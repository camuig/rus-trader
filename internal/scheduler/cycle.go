package scheduler

import (
	"context"
	"fmt"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/camuig/rus-trader/internal/ai"
	"github.com/camuig/rus-trader/internal/broker"
	"github.com/camuig/rus-trader/internal/dividends"
	"github.com/camuig/rus-trader/internal/features"
	"github.com/camuig/rus-trader/internal/guard"
	"github.com/camuig/rus-trader/internal/moex"
	"github.com/camuig/rus-trader/internal/orderbook"
	"github.com/camuig/rus-trader/internal/screener"
	"github.com/camuig/rus-trader/internal/sentiment"
)

// cycleState holds all data accumulated during a single trading cycle.
type cycleState struct {
	tradableTickers []string
	allSnapshots    []broker.CandleSnapshot
	snapshots       []broker.CandleSnapshot
	portfolio       *broker.PortfolioInfo
	tickerNews      map[string][]moex.NewsItem
	globalNews      []string
	divMap          map[string]dividends.DividendInfo
	obMetrics       map[string]*orderbook.OrderBookMetrics
	sentimentMap    map[string]*sentiment.SentimentResult
	featuresMap     map[string]string
	featuresList    []string
	patterns        map[string][]ai.PatternMatchInfo
	openContext     map[string]ai.OpenTradeContext
	todayTraded     []string
	stats           ai.PerformanceStats
	marketCtx       ai.MarketContext
	lessonLines     []string
	decisions       []ai.AIDecision
	rawResponse     string
}

func (s *Scheduler) runCycle(ctx context.Context) (ok bool) {
	defer func() {
		if r := recover(); r != nil {
			s.logger.Error("panic in scheduler cycle", "panic", fmt.Sprint(r))
			s.notifier.NotifyError("scheduler panic", fmt.Errorf("%v", r))
			ok = false
		}
	}()

	if !s.isWithinTradingHours() {
		s.logger.Info("outside trading hours, skipping cycle")
		return true
	}

	state := &cycleState{}

	if !s.collectData(ctx, state) {
		return false
	}

	s.enrichData(ctx, state)

	s.runAgents(ctx, state)

	s.applyGuardAndExecute(state)

	return true
}

// collectData fetches market data, portfolio, news. Returns false if a critical step fails.
func (s *Scheduler) collectData(ctx context.Context, state *cycleState) bool {
	s.logger.Info("starting analysis cycle")

	// 1. Fetch top tickers from MOEX. Берём с запасом — чтобы пул под пре-фильтр
	// был достаточным даже после отсеивания не-торгуемых.
	topN := s.config.Trading.CandidatePoolSize + 30
	if topN < 50 {
		topN = 50
	}
	topTickers, err := s.moex.FetchTopTickers(ctx, topN)
	if err != nil {
		s.logger.Error("fetch top tickers", "error", err)
		s.saveAnalysisLog(0, "", "", err)
		return false
	}
	s.logger.Info("top tickers fetched", "count", len(topTickers))

	// 2. Resolve tickers to UIDs and filter tradable
	tickerNames := make([]string, len(topTickers))
	for i, t := range topTickers {
		tickerNames[i] = t.Ticker
	}

	uids := make([]string, 0, len(tickerNames))
	uidToTicker := make(map[string]string, len(tickerNames))
	for _, t := range tickerNames {
		uid, err := s.broker.ResolveTickerToUID(t)
		if err != nil {
			s.logger.Debug("resolve ticker failed, skipping", "ticker", t, "error", err)
			continue
		}
		uids = append(uids, uid)
		uidToTicker[uid] = t
	}

	tradable, err := s.broker.FilterTradable(uids)
	if err != nil {
		s.logger.Error("filter tradable", "error", err)
		s.saveAnalysisLog(len(topTickers), "", "", err)
		return false
	}

	// Пул кандидатов для расчёта индикаторов (до пре-фильтра и Screener).
	// Должен быть больше MaxAnalysisTickers, чтобы после отсечения жёстких BUY-фильтров
	// оставалось достаточно тикеров для ИИ.
	poolSize := s.config.Trading.CandidatePoolSize
	state.tradableTickers = make([]string, 0, poolSize)
	tradableSet := make(map[string]bool)
	for _, uid := range uids {
		if tradable[uid] {
			t := uidToTicker[uid]
			tradableSet[t] = true
			if len(state.tradableTickers) < poolSize {
				state.tradableTickers = append(state.tradableTickers, t)
			}
		}
	}
	s.logger.Info("tradable tickers", "total", len(tradableSet), "pool", len(state.tradableTickers))

	// 3. Get portfolio
	state.portfolio, err = s.broker.GetPortfolio()
	if err != nil {
		s.logger.Error("get portfolio", "error", err)
		s.saveAnalysisLog(len(topTickers), "", "", err)
		return false
	}

	s.reconcileOrphanTrades(state.portfolio)
	s.adoptOrphanPositions(state.portfolio)

	// 4. Include position tickers
	for _, pos := range state.portfolio.Positions {
		if pos.Ticker != "" && !tradableSet[pos.Ticker] {
			tradableSet[pos.Ticker] = true
			state.tradableTickers = append(state.tradableTickers, pos.Ticker)
			s.logger.Info("added position ticker to analysis", "ticker", pos.Ticker)
		}
	}

	if len(state.tradableTickers) == 0 {
		s.logger.Info("no tradable tickers, skipping cycle")
		return false
	}

	// 5. Fetch candle snapshots
	concurrency := s.config.Trading.CandleConcurrency
	state.allSnapshots = s.broker.FetchCandleSnapshots(state.tradableTickers, concurrency)
	s.logger.Info("candle snapshots fetched", "count", len(state.allSnapshots))

	// 5.1. Санити-фильтр: аномальные котировки (кейс EUTR: -99% за день в песочнице)
	// не должны попадать ни в AI, ни в ордера.
	state.allSnapshots = s.filterAnomalousSnapshots(state.allSnapshots)

	// 5a. Screen tickers
	positionTickers := make(map[string]bool, len(state.portfolio.Positions))
	for _, pos := range state.portfolio.Positions {
		positionTickers[pos.Ticker] = true
	}

	// 5a.1. Pre-screen: отсечь не-позиционные тикеры, которые заведомо не пройдут
	// жёсткие BUY-фильтры (ATR%, RSI, uptrend, cooldown, recent_loss, streak).
	// Это экономит слоты MaxAnalysisTickers: ИИ анализирует только реально покупаемых кандидатов.
	preScreen := s.guard.PreScreenBuyable(state.allSnapshots, positionTickers)
	if len(preScreen.Blocked) > 0 {
		// Агрегированная разбивка по причинам — видим, какой фильтр доминирует,
		// и можем осознанно ослаблять параметры, а не гадать.
		s.logger.Info("pre-screen breakdown",
			"blocked_total", len(preScreen.Blocked),
			"allowed", len(preScreen.Allowed),
			"cooldown", preScreen.BlockCounts[guard.BlockReasonCooldown],
			"recent_loss", preScreen.BlockCounts[guard.BlockReasonRecentLoss],
			"losing_streak", preScreen.BlockCounts[guard.BlockReasonLosingStreak],
			"rsi_overbought", preScreen.BlockCounts[guard.BlockReasonRSI],
			"downtrend", preScreen.BlockCounts[guard.BlockReasonDowntrend],
			"low_atr", preScreen.BlockCounts[guard.BlockReasonLowATR])
		for ticker, reason := range preScreen.Blocked {
			s.logger.Debug("pre-screen block", "ticker", ticker, "reason", reason)
		}
	}

	state.snapshots = screener.Screen(preScreen.Allowed, positionTickers, s.config.Trading.MaxAnalysisTickers, s.config.Trading.MinScreenerScore)
	s.logger.Info("screened tickers",
		"pool", len(state.allSnapshots),
		"after_pre_screen", len(preScreen.Allowed),
		"blocked_by_pre_screen", len(preScreen.Blocked),
		"to_ai", len(state.snapshots))

	// Log screener scores
	scores := screener.Scores(state.allSnapshots)
	for _, sc := range scores {
		for _, snap := range state.allSnapshots {
			if snap.Ticker == sc.Ticker {
				ind := snap.Indicators
				s.logger.Debug("ticker indicators",
					"ticker", sc.Ticker, "score", sc.Points,
					"rsi", fmt.Sprintf("%.1f", ind.RSI14),
					"ema9", fmt.Sprintf("%.2f", ind.EMA9),
					"ema21", fmt.Sprintf("%.2f", ind.EMA21),
					"atr", fmt.Sprintf("%.2f", ind.ATR14),
					"relVol", fmt.Sprintf("%.1f", ind.RelVolume),
					"price", fmt.Sprintf("%.2f", snap.LastPrice))
				break
			}
		}
	}

	// 6. Fetch news
	finamNews, err := s.moex.FetchFinamNews(ctx, 30)
	if err != nil {
		s.logger.Error("fetch finam news", "error", err)
		finamNews = nil
	}
	state.tickerNews = moex.FilterNewsForTickers(finamNews, state.tradableTickers)
	s.logger.Info("finam news fetched", "total", len(finamNews), "matched_tickers", len(state.tickerNews))

	worldCtx, cancelWorld := context.WithTimeout(ctx, 8*time.Second)
	worldNewsItems, err := s.moex.FetchWorldNews(worldCtx, s.config.LLM.MaxWorldNewsItems*2)
	cancelWorld()
	if err != nil {
		s.logger.Error("fetch world news", "error", err)
		worldNewsItems = nil
	}
	state.globalNews = make([]string, 0, len(worldNewsItems)+len(finamNews))
	for _, n := range finamNews {
		state.globalNews = append(state.globalNews, fmt.Sprintf("Финам: %s", n.Title))
	}
	for _, n := range worldNewsItems {
		if n.Source != "" {
			state.globalNews = append(state.globalNews, fmt.Sprintf("%s: %s", n.Source, n.Title))
		} else {
			state.globalNews = append(state.globalNews, n.Title)
		}
	}
	s.logger.Info("news fetched", "finam", len(finamNews), "world", len(worldNewsItems))

	// 8. Open trade context
	state.openContext = make(map[string]ai.OpenTradeContext)
	if openTrades, err := s.repo.GetOpenTrades(); err == nil {
		for _, t := range openTrades {
			state.openContext[t.Ticker] = ai.OpenTradeContext{
				Reasoning:       t.Reasoning,
				OpenedAt:        t.CreatedAt,
				StopLossPrice:   t.StopLossPrice,
				TakeProfitPrice: t.TakeProfitPrice,
			}
		}
	}

	state.todayTraded, _ = s.repo.GetTodayTradedTickers()
	state.stats = s.fetchPerformanceStats()
	state.marketCtx = computeMarketContext(state.allSnapshots)

	s.logger.Info("market context",
		"regime", state.marketCtx.Regime,
		"chg_1d", fmt.Sprintf("%+.2f%%", state.marketCtx.ChangePct1d),
		"chg_3d", fmt.Sprintf("%+.2f%%", state.marketCtx.ChangePct3d),
		"chg_1w", fmt.Sprintf("%+.2f%%", state.marketCtx.ChangePct1w))

	return true
}

// enrichData adds journal lessons, dividends, order book, sentiment, features, vector patterns.
func (s *Scheduler) enrichData(ctx context.Context, state *cycleState) {
	// Daily review
	s.runDailyReview(ctx)

	// Journal lessons
	if s.journal != nil && s.config.Journal.Enabled {
		lessons, err := s.journal.LoadLessons(s.config.Journal.MaxLessonAgeDays, s.config.Journal.MaxLessonsInPrompt)
		if err != nil {
			s.logger.Error("load journal lessons", "error", err)
		}
		for _, l := range lessons {
			state.lessonLines = append(state.lessonLines, fmt.Sprintf("[%s] %s: %s → %s", l.Confidence, l.Pattern, l.Observation, l.Recommendation))
		}
		if len(state.lessonLines) > 0 {
			s.logger.Info("journal lessons loaded", "count", len(state.lessonLines))
		}
	}

	// Dividends
	if s.divFetcher != nil && s.config.Dividends.Enabled {
		allDivs, err := s.divFetcher.Fetch(ctx)
		if err != nil {
			s.logger.Error("fetch dividends", "error", err)
		} else {
			state.divMap = dividends.FilterByLookahead(allDivs, s.config.Dividends.LookaheadDays)
			s.logger.Info("dividends fetched", "total", len(allDivs), "relevant", len(state.divMap))
		}
	}

	// Order book
	state.obMetrics = s.fetchOrderBooks(ctx, state.snapshots)

	// Sentiment
	var screenerTickers []string
	for _, snap := range state.snapshots {
		screenerTickers = append(screenerTickers, snap.Ticker)
	}
	state.sentimentMap = s.scoreSentiment(ctx, state.tickerNews, screenerTickers)

	// Build textual features.
	// featuresMap (по ticker) — нужен Position Manager и пост-фильтру, включает ВСЕ тикеры.
	// featuresList — уходит в Screening Agent (поиск новых BUY); позиционные тикеры в него
	// НЕ включаем: скрининг не должен предлагать BUY на уже открытые позиции (усреднение
	// — задача Position Manager, и оно обычно блокируется по EMA в нисходящем тренде).
	positionTickers := make(map[string]bool, len(state.portfolio.Positions))
	for _, pos := range state.portfolio.Positions {
		positionTickers[pos.Ticker] = true
	}
	state.featuresMap = make(map[string]string, len(state.snapshots))
	for _, snap := range state.snapshots {
		var divPtr *dividends.DividendInfo
		if d, ok := state.divMap[snap.Ticker]; ok {
			divPtr = &d
		}
		var obPtr *orderbook.OrderBookMetrics
		if m, ok := state.obMetrics[snap.Ticker]; ok {
			obPtr = m
		}
		var sentPtr *sentiment.SentimentResult
		if r, ok := state.sentimentMap[snap.Ticker]; ok {
			sentPtr = r
		}
		tf := features.BuildTickerFeatures(snap, divPtr, obPtr, sentPtr)
		state.featuresMap[tf.Ticker] = tf.Summary
		if !positionTickers[snap.Ticker] {
			state.featuresList = append(state.featuresList, tf.Summary)
		}
	}
	s.executor.SetFeatures(state.featuresMap)

	// Vector search
	state.patterns = s.findSimilarPatterns(ctx, state.featuresMap)
}

// runAgents calls Screening Agent and Position Manager in parallel.
func (s *Scheduler) runAgents(ctx context.Context, state *cycleState) {
	// Проверяем глобальные гейты: если новых позиций открыть нельзя,
	// screening agent бесполезен — не тратим токены, работает только Position Manager.
	canBuy, buyBlockReason := s.guard.CanOpenNewPositions()
	if !canBuy {
		s.logger.Info("skipping screening agent: no new buys allowed", "reason", buyBlockReason)
	}

	s.logger.Info("starting AI analysis",
		"screening_enabled", canBuy,
		"screening_tickers", len(state.featuresList),
		"open_positions", len(state.portfolio.Positions))

	// Build ticker news map
	tickerNewsMap := make(map[string][]string)
	for ticker, items := range state.tickerNews {
		for _, n := range items {
			tickerNewsMap[ticker] = append(tickerNewsMap[ticker], n.Title)
		}
	}

	var screeningDecisions, positionDecisions []ai.AIDecision
	var screeningRaw, positionRaw string

	g, gctx := errgroup.WithContext(ctx)

	// Screening Agent — только если открывать новые позиции в принципе разрешено
	if canBuy {
		g.Go(func() error {
			screeningReq := &ai.ScreeningRequest{
				TickerFeatures:  state.featuresList,
				Market:          state.marketCtx,
				GlobalNews:      state.globalNews,
				TickerNews:      tickerNewsMap,
				Lessons:         state.lessonLines,
				TodayTraded:     state.todayTraded,
				Stats:           state.stats,
				CurrentTime:     time.Now().In(s.loc),
				AvailableRub:    state.portfolio.AvailableRub,
				SimilarPatterns: state.patterns,
			}
			decs, raw, err := s.ai.ScreeningAnalyze(gctx, screeningReq)
			if err != nil {
				s.logger.Error("screening agent failed", "error", err)
				screeningRaw = raw
				return nil
			}
			screeningDecisions = decs
			screeningRaw = raw
			s.logger.Info("screening agent done", "decisions", len(decs))
			return nil
		})
	}

	// Position Manager
	if len(state.portfolio.Positions) > 0 {
		g.Go(func() error {
			var posContexts []ai.PositionContext
			for _, pos := range state.portfolio.Positions {
				pctChange := 0.0
				if pos.AvgPrice > 0 {
					pctChange = (pos.CurrentPrice - pos.AvgPrice) / pos.AvgPrice * 100
				}
				pc := ai.PositionContext{
					Ticker:       pos.Ticker,
					EntryPrice:   pos.AvgPrice,
					CurrentPrice: pos.CurrentPrice,
					PnLPct:       pctChange,
					Quantity:     int64(pos.Quantity),
					Features:     state.featuresMap[pos.Ticker],
				}
				if tc, ok := state.openContext[pos.Ticker]; ok {
					pc.StopLoss = tc.StopLossPrice
					pc.TakeProfit = tc.TakeProfitPrice
					pc.Hypothesis = tc.Reasoning
					pc.HoldDuration = formatDurationSince(tc.OpenedAt)
					if tc.TakeProfitPrice > 0 && pos.AvgPrice > 0 && tc.TakeProfitPrice != pos.AvgPrice {
						pc.ProgressToTP = (pos.CurrentPrice - pos.AvgPrice) / (tc.TakeProfitPrice - pos.AvgPrice) * 100
					}
				}
				if items, ok := tickerNewsMap[pos.Ticker]; ok {
					pc.News = items
				}
				posContexts = append(posContexts, pc)
			}

			posReq := &ai.PositionRequest{
				Positions:   posContexts,
				Lessons:     state.lessonLines,
				CurrentTime: time.Now().In(s.loc),
				Market:      state.marketCtx,
			}
			decs, raw, err := s.ai.PositionAnalyze(gctx, posReq)
			if err != nil {
				s.logger.Error("position manager failed", "error", err)
				positionRaw = raw
				return nil
			}
			positionDecisions = decs
			positionRaw = raw
			s.logger.Info("position manager done", "decisions", len(decs))
			return nil
		})
	}

	_ = g.Wait()

	// Merge decisions
	state.decisions = append(positionDecisions, screeningDecisions...)
	if screeningRaw != "" && positionRaw != "" {
		state.rawResponse = "=== SCREENING ===\n" + screeningRaw + "\n=== POSITION MANAGER ===\n" + positionRaw
	} else if screeningRaw != "" {
		state.rawResponse = screeningRaw
	} else {
		state.rawResponse = positionRaw
	}

	s.logger.Info("AI decisions received", "count", len(state.decisions),
		"screening", len(screeningDecisions), "position", len(positionDecisions))

	if len(state.decisions) == 0 {
		s.logger.Info("AI returned no decisions",
			"tickers_screened", len(state.snapshots),
			"available_rub", state.portfolio.AvailableRub,
			"open_positions", len(state.portfolio.Positions))
	}

	// Log breakdown
	var buys, sells, holds int
	for _, d := range state.decisions {
		switch d.Action {
		case "BUY":
			buys++
		case "SELL":
			sells++
		case "HOLD":
			holds++
		}
		s.logger.Info("AI decision",
			"action", d.Action, "ticker", d.Ticker,
			"confidence", d.Confidence, "reasoning", d.Reasoning)
	}
	if len(state.decisions) > 0 {
		s.logger.Info("decisions breakdown", "buy", buys, "sell", sells, "hold", holds)
	}
}
