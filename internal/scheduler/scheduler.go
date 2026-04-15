package scheduler

import (
	"context"
	"fmt"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/camuig/rus-trader/internal/ai"
	"github.com/camuig/rus-trader/internal/broker"
	"github.com/camuig/rus-trader/internal/config"
	"github.com/camuig/rus-trader/internal/dividends"
	"github.com/camuig/rus-trader/internal/executor"
	"github.com/camuig/rus-trader/internal/features"
	"github.com/camuig/rus-trader/internal/guard"
	"github.com/camuig/rus-trader/internal/indicators"
	"github.com/camuig/rus-trader/internal/journal"
	"github.com/camuig/rus-trader/internal/logger"
	"github.com/camuig/rus-trader/internal/moex"
	"github.com/camuig/rus-trader/internal/orderbook"
	"github.com/camuig/rus-trader/internal/screener"
	"github.com/camuig/rus-trader/internal/sentiment"
	"github.com/camuig/rus-trader/internal/storage"
	"github.com/camuig/rus-trader/internal/telegram"
	"github.com/camuig/rus-trader/internal/vectordb"
)

type Scheduler struct {
	broker      *broker.BrokerClient
	moex        *moex.Client
	ai          *ai.DeepSeekClient
	executor    *executor.Executor
	repo        *storage.Repository
	notifier    *telegram.Notifier
	guard       *guard.TradeGuard
	config      *config.Config
	logger      *logger.Logger
	loc         *time.Location
	divFetcher  *dividends.Fetcher
	journal     *journal.Journal
	sentiment   *sentiment.Scorer
	vectorStore *vectordb.Store
}

func NewScheduler(
	bc *broker.BrokerClient,
	moexClient *moex.Client,
	aiClient *ai.DeepSeekClient,
	exec *executor.Executor,
	repo *storage.Repository,
	notifier *telegram.Notifier,
	g *guard.TradeGuard,
	cfg *config.Config,
	log *logger.Logger,
	divFetcher *dividends.Fetcher,
	j *journal.Journal,
	sentScorer *sentiment.Scorer,
	vs *vectordb.Store,
) *Scheduler {
	return &Scheduler{
		broker:      bc,
		moex:        moexClient,
		ai:          aiClient,
		executor:    exec,
		repo:        repo,
		notifier:    notifier,
		guard:       g,
		config:      cfg,
		logger:      log,
		loc:         cfg.MOEXLocation(),
		divFetcher:  divFetcher,
		journal:     j,
		sentiment:   sentScorer,
		vectorStore: vs,
	}
}

const retryDelay = 30 * time.Second

func (s *Scheduler) Run(ctx context.Context) {
	interval := s.config.TradingInterval()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	s.logger.Info("scheduler started", "interval", interval.String())

	// Run immediately on start
	s.runWithRetry(ctx)

	for {
		select {
		case <-ctx.Done():
			s.logger.Info("scheduler stopped")
			return
		case <-ticker.C:
			s.runWithRetry(ctx)
		}
	}
}

func (s *Scheduler) runWithRetry(ctx context.Context) {
	if s.runCycle(ctx) {
		return
	}
	s.logger.Info("cycle failed, retrying", "delay", retryDelay)
	select {
	case <-ctx.Done():
	case <-time.After(retryDelay):
		s.runCycle(ctx)
	}
}

// runCycle returns true if the cycle completed successfully, false if it failed and should be retried.
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
		return true // not an error, no retry needed
	}

	s.logger.Info("starting analysis cycle")

	// 1. Fetch top tickers from MOEX (fetch more, filter later)
	topTickers, err := s.moex.FetchTopTickers(ctx, 50)
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

	// Collect tradable tickers preserving MOEX volume ranking order
	maxTickers := s.config.Trading.MaxAnalysisTickers
	tradableTickers := make([]string, 0, maxTickers)
	tradableSet := make(map[string]bool)
	for _, uid := range uids {
		if tradable[uid] {
			t := uidToTicker[uid]
			tradableSet[t] = true
			if len(tradableTickers) < maxTickers {
				tradableTickers = append(tradableTickers, t)
			}
		}
	}
	s.logger.Info("tradable tickers", "total", len(tradableSet), "selected", len(tradableTickers))

	// 3. Get portfolio early — we need position tickers before fetching candles
	portfolio, err := s.broker.GetPortfolio()
	if err != nil {
		s.logger.Error("get portfolio", "error", err)
		s.saveAnalysisLog(len(topTickers), "", "", err)
		return false
	}

	// 3a. Reconcile: close DB trades that no longer exist at broker (stale orphans).
	s.reconcileOrphanTrades(portfolio)

	// 4. Ensure tickers with open positions are always included (even beyond limit)
	for _, pos := range portfolio.Positions {
		if pos.Ticker != "" && !tradableSet[pos.Ticker] {
			tradableSet[pos.Ticker] = true
			tradableTickers = append(tradableTickers, pos.Ticker)
			s.logger.Info("added position ticker to analysis", "ticker", pos.Ticker)
		}
	}

	if len(tradableTickers) == 0 {
		s.logger.Info("no tradable tickers, skipping cycle")
		return true
	}

	// 5. Fetch candle snapshots
	concurrency := s.config.Trading.CandleConcurrency
	allSnapshots := s.broker.FetchCandleSnapshots(tradableTickers, concurrency)
	s.logger.Info("candle snapshots fetched", "count", len(allSnapshots))

	// 5a. Screen tickers by technical signals (keep positions, filter rest by score)
	positionTickers := make(map[string]bool, len(portfolio.Positions))
	for _, pos := range portfolio.Positions {
		positionTickers[pos.Ticker] = true
	}
	snapshots := screener.Screen(allSnapshots, positionTickers, s.config.Trading.MaxAnalysisTickers, s.config.Trading.MinScreenerScore)
	s.logger.Info("screened tickers", "before", len(allSnapshots), "after", len(snapshots))

	// Log indicators for all screened tickers
	scores := screener.Scores(allSnapshots)
	for _, sc := range scores {
		for _, snap := range allSnapshots {
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

	// 6. Fetch Finam company news (non-fatal)
	finamNews, err := s.moex.FetchFinamNews(ctx, 30)
	if err != nil {
		s.logger.Error("fetch finam news", "error", err)
		finamNews = nil
	}
	tickerNews := moex.FilterNewsForTickers(finamNews, tradableTickers)
	s.logger.Info("finam news fetched", "total", len(finamNews), "matched_tickers", len(tickerNews))

	// 7. Fetch world news (non-fatal)
	worldCtx, cancelWorld := context.WithTimeout(ctx, 8*time.Second)
	worldNewsItems, err := s.moex.FetchWorldNews(worldCtx, s.config.DeepSeek.MaxWorldNewsItems*2)
	cancelWorld()
	if err != nil {
		s.logger.Error("fetch world news", "error", err)
		worldNewsItems = nil
	}
	globalNews := make([]string, 0, len(worldNewsItems)+len(finamNews))
	// Add Finam news to global context
	for _, n := range finamNews {
		globalNews = append(globalNews, fmt.Sprintf("Финам: %s", n.Title))
	}
	for _, n := range worldNewsItems {
		if n.Source != "" {
			globalNews = append(globalNews, fmt.Sprintf("%s: %s", n.Source, n.Title))
		} else {
			globalNews = append(globalNews, n.Title)
		}
	}
	s.logger.Info("news fetched", "finam", len(finamNews), "world", len(worldNewsItems))

	// 8. Fetch open trade context for AI (including SL/TP plan)
	openContext := make(map[string]ai.OpenTradeContext)
	if openTrades, err := s.repo.GetOpenTrades(); err == nil {
		for _, t := range openTrades {
			openContext[t.Ticker] = ai.OpenTradeContext{
				Reasoning:       t.Reasoning,
				OpenedAt:        t.CreatedAt,
				StopLossPrice:   t.StopLossPrice,
				TakeProfitPrice: t.TakeProfitPrice,
			}
		}
	}

	// 9. Fetch today's traded tickers for anti-churning
	todayTraded, _ := s.repo.GetTodayTradedTickers()

	// 10. Fetch performance stats for AI context
	stats := s.fetchPerformanceStats()

	// 11. Market regime context derived from all snapshots (no extra API calls).
	marketCtx := computeMarketContext(allSnapshots)
	s.logger.Info("market context",
		"regime", marketCtx.Regime,
		"chg_1d", fmt.Sprintf("%+.2f%%", marketCtx.ChangePct1d),
		"chg_3d", fmt.Sprintf("%+.2f%%", marketCtx.ChangePct3d),
		"chg_1w", fmt.Sprintf("%+.2f%%", marketCtx.ChangePct1w))

	// 12. Daily journal review (first cycle of the day)
	s.runDailyReview(ctx)

	// 13. Load journal lessons
	var lessonLines []string
	if s.journal != nil && s.config.Journal.Enabled {
		lessons, err := s.journal.LoadLessons(s.config.Journal.MaxLessonAgeDays, s.config.Journal.MaxLessonsInPrompt)
		if err != nil {
			s.logger.Error("load journal lessons", "error", err)
		}
		for _, l := range lessons {
			lessonLines = append(lessonLines, fmt.Sprintf("[%s] %s: %s → %s", l.Confidence, l.Pattern, l.Observation, l.Recommendation))
		}
		if len(lessonLines) > 0 {
			s.logger.Info("journal lessons loaded", "count", len(lessonLines))
		}
	}

	// 14. Fetch dividends (if enabled)
	var divMap map[string]dividends.DividendInfo
	if s.divFetcher != nil && s.config.Dividends.Enabled {
		allDivs, err := s.divFetcher.Fetch(ctx)
		if err != nil {
			s.logger.Error("fetch dividends", "error", err)
		} else {
			divMap = dividends.FilterByLookahead(allDivs, s.config.Dividends.LookaheadDays)
			s.logger.Info("dividends fetched", "total", len(allDivs), "relevant", len(divMap))
		}
	}

	// 15. Fetch order book metrics for screened tickers (parallel)
	obMetrics := s.fetchOrderBooks(ctx, snapshots)

	// 15a. Collect screened ticker names for sentiment
	var screenerTickers []string
	for _, snap := range snapshots {
		screenerTickers = append(screenerTickers, snap.Ticker)
	}

	// 15b. Score sentiment (LLM, non-fatal)
	sentimentMap := s.scoreSentiment(ctx, tickerNews, screenerTickers)

	// 15c. Build textual features with all data sources
	featuresMap := make(map[string]string, len(snapshots))
	var tickerFeaturesList []string
	for _, snap := range snapshots {
		var divPtr *dividends.DividendInfo
		if d, ok := divMap[snap.Ticker]; ok {
			divPtr = &d
		}
		var obPtr *orderbook.OrderBookMetrics
		if m, ok := obMetrics[snap.Ticker]; ok {
			obPtr = m
		}
		var sentPtr *sentiment.SentimentResult
		if r, ok := sentimentMap[snap.Ticker]; ok {
			sentPtr = r
		}
		tf := features.BuildTickerFeatures(snap, divPtr, obPtr, sentPtr)
		featuresMap[tf.Ticker] = tf.Summary
		tickerFeaturesList = append(tickerFeaturesList, tf.Summary)
	}
	s.executor.SetFeatures(featuresMap)

	// 15d. Find similar historical patterns (vector search)
	similarPatterns := s.findSimilarPatterns(ctx, featuresMap)

	// 16. Build ticker news map (map[string][]string)
	tickerNewsMap := make(map[string][]string)
	for ticker, items := range tickerNews {
		for _, n := range items {
			tickerNewsMap[ticker] = append(tickerNewsMap[ticker], n.Title)
		}
	}

	// 17. Dual AI agents (screening + position manager) in parallel
	var screeningDecisions, positionDecisions []ai.AIDecision
	var screeningRaw, positionRaw string

	g, gctx := errgroup.WithContext(ctx)

	// Screening Agent: find BUY candidates
	g.Go(func() error {
		screeningReq := &ai.ScreeningRequest{
			TickerFeatures:  tickerFeaturesList,
			Market:          marketCtx,
			GlobalNews:      globalNews,
			TickerNews:      tickerNewsMap,
			Lessons:         lessonLines,
			TodayTraded:     todayTraded,
			Stats:           stats,
			CurrentTime:     time.Now().In(s.loc),
			AvailableRub:    portfolio.AvailableRub,
			SimilarPatterns: similarPatterns,
		}
		decs, raw, err := s.ai.ScreeningAnalyze(gctx, screeningReq)
		if err != nil {
			s.logger.Error("screening agent failed", "error", err)
			screeningRaw = raw
			return nil // don't fail the group
		}
		screeningDecisions = decs
		screeningRaw = raw
		s.logger.Info("screening agent done", "decisions", len(decs))
		return nil
	})

	// Position Manager: manage open positions (only if positions exist)
	if len(portfolio.Positions) > 0 {
		g.Go(func() error {
			var posContexts []ai.PositionContext
			for _, pos := range portfolio.Positions {
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
					Features:     featuresMap[pos.Ticker],
				}
				if tc, ok := openContext[pos.Ticker]; ok {
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
				Lessons:     lessonLines,
				CurrentTime: time.Now().In(s.loc),
				Market:      marketCtx,
			}
			decs, raw, err := s.ai.PositionAnalyze(gctx, posReq)
			if err != nil {
				s.logger.Error("position manager failed", "error", err)
				positionRaw = raw
				return nil // don't fail the group
			}
			positionDecisions = decs
			positionRaw = raw
			s.logger.Info("position manager done", "decisions", len(decs))
			return nil
		})
	}

	_ = g.Wait()

	// Merge decisions: position manager first, then screening
	decisions := append(positionDecisions, screeningDecisions...)
	rawResponse := ""
	if screeningRaw != "" && positionRaw != "" {
		rawResponse = "=== SCREENING ===\n" + screeningRaw + "\n=== POSITION MANAGER ===\n" + positionRaw
	} else if screeningRaw != "" {
		rawResponse = screeningRaw
	} else {
		rawResponse = positionRaw
	}

	s.logger.Info("AI decisions received", "count", len(decisions),
		"screening", len(screeningDecisions), "position", len(positionDecisions))

	if len(decisions) == 0 {
		s.logger.Info("AI returned no decisions — no trading signals found",
			"tickers_screened", len(snapshots),
			"available_rub", portfolio.AvailableRub,
			"open_positions", len(portfolio.Positions))
	}

	// Log decisions breakdown
	var buys, sells, holds int
	for _, d := range decisions {
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
	if len(decisions) > 0 {
		s.logger.Info("decisions breakdown",
			"buy", buys, "sell", sells, "hold", holds)
	}

	// 13. Set indicators in guard for pre-validation and apply filter
	indicatorsMap := make(map[string]indicators.Indicators, len(snapshots))
	for _, snap := range snapshots {
		indicatorsMap[snap.Ticker] = snap.Indicators
	}
	s.guard.SetIndicators(indicatorsMap)
	s.executor.SetIndicators(indicatorsMap)
	allowed, blocked := s.guard.Filter(decisions)
	for _, b := range blocked {
		s.logger.Info("decision BLOCKED by guard",
			"ticker", b.Decision.Ticker, "action", b.Decision.Action,
			"reason", b.Reason, "confidence", b.Decision.Confidence)
		s.notifier.NotifyBlocked(b.Decision.Ticker, b.Decision.Action, b.Reason)
	}

	allowedDecisions := make([]ai.AIDecision, len(allowed))
	for i, a := range allowed {
		allowedDecisions[i] = a.Decision
	}
	s.logger.Info("guard filter applied",
		"allowed", len(allowedDecisions), "blocked", len(blocked))

	if len(allowedDecisions) == 0 && len(decisions) > 0 {
		s.logger.Info("all decisions were blocked or HOLD — no trades will be executed")
	}

	// 13a. Update trailing stops for open positions
	if s.config.Trading.TrailingStopEnabled {
		s.updateTrailingStops(portfolio)
	}

	// 14. Execute decisions
	s.executor.Execute(allowedDecisions)

	// 12. Save analysis log and portfolio snapshot
	s.saveAnalysisLog(len(tradableTickers), rawResponse, executor.DecisionsToJSON(decisions), nil)
	s.savePortfolioSnapshot(portfolio)

	s.logger.Info("analysis cycle completed")
	return true
}






func (s *Scheduler) updateTrailingStops(portfolio *broker.PortfolioInfo) {
	openTrades, err := s.repo.GetOpenTrades()
	if err != nil {
		s.logger.Error("trailing stop: get open trades", "error", err)
		return
	}

	cfg := s.config.Trading
	breakevenPct := cfg.TrailingBreakevenPct / 100  // e.g., 0.50
	lockProfitPct := cfg.TrailingLockProfitPct / 100 // e.g., 0.75

	for _, trade := range openTrades {
		if trade.TakeProfitPrice <= 0 || trade.StopLossPrice <= 0 || trade.Price <= 0 {
			continue
		}

		// Find current price from portfolio
		var currentPrice float64
		for _, pos := range portfolio.Positions {
			if pos.Ticker == trade.Ticker {
				currentPrice = pos.CurrentPrice
				break
			}
		}
		if currentPrice <= 0 {
			continue
		}

		tpDistance := trade.TakeProfitPrice - trade.Price
		if tpDistance <= 0 {
			continue
		}

		progress := (currentPrice - trade.Price) / tpDistance // 0 to 1+
		var newSL float64

		if progress >= lockProfitPct {
			// Lock 50% of current profit
			profit := currentPrice - trade.Price
			newSL = trade.Price + profit*0.5
		} else if progress >= breakevenPct {
			// Move SL to breakeven (entry price + small buffer)
			newSL = trade.Price * 1.001
		}

		if newSL <= 0 || newSL <= trade.StopLossPrice {
			continue // only move SL up, never down
		}

		s.logger.Info("trailing stop: updating SL",
			"ticker", trade.Ticker, "oldSL", trade.StopLossPrice,
			"newSL", newSL, "progress", fmt.Sprintf("%.0f%%", progress*100))

		// Cancel old SL and place new one
		instrumentUID, err := s.broker.ResolveTickerToUID(trade.Ticker)
		if err != nil {
			s.logger.Error("trailing stop: resolve ticker", "ticker", trade.Ticker, "error", err)
			continue
		}

		if trade.StopLossOrderID != "" {
			s.broker.CancelStopOrders(trade.StopLossOrderID, "")
		}

		newSLOrderID, err := s.broker.PlaceStopLoss(instrumentUID, trade.Quantity, newSL)
		if err != nil {
			s.logger.Error("trailing stop: place new SL", "ticker", trade.Ticker, "error", err)
			continue
		}

		trade.StopLossPrice = newSL
		trade.StopLossOrderID = newSLOrderID
		if err := s.repo.UpdateTrade(&trade); err != nil {
			s.logger.Error("trailing stop: update trade", "error", err)
		}
	}
}


func (s *Scheduler) runDailyReview(ctx context.Context) {
	if s.journal == nil || !s.config.Journal.Enabled {
		return
	}
	if !s.journal.NeedsDailyReview(s.loc) {
		return
	}

	outcomes, err := s.journal.GetPendingOutcomes()
	if err != nil {
		s.logger.Error("daily review: get outcomes", "error", err)
		return
	}
	if len(outcomes) < s.config.Journal.MinTradesForReview {
		s.logger.Info("daily review: not enough trades", "count", len(outcomes), "min", s.config.Journal.MinTradesForReview)
		return
	}

	s.logger.Info("running daily journal review", "outcomes", len(outcomes))

	// Build stats
	var wins, losses int
	var winPnL, lossPnL, winHours, lossHours float64
	exitReasons := make(map[string]int)
	var journalOutcomes []ai.JournalOutcome

	for _, o := range outcomes {
		journalOutcomes = append(journalOutcomes, ai.JournalOutcome{
			Hypothesis:    o.Hypothesis,
			EntryFeatures: o.EntryFeatures,
			ExitReason:    o.ExitReason,
			Outcome:       o.Outcome,
			PnL:           o.PnL,
			HoldHours:     o.HoldDurationHours,
			WhatHappened:  o.WhatHappened,
		})
		exitReasons[o.ExitReason]++
		if o.PnL > 0 {
			wins++
			winPnL += o.PnL
			winHours += o.HoldDurationHours
		} else if o.PnL < 0 {
			losses++
			lossPnL += o.PnL
			lossHours += o.HoldDurationHours
		}
	}

	total := wins + losses
	winRate := 0.0
	if total > 0 {
		winRate = float64(wins) / float64(total) * 100
	}

	// Load previous lessons for continuity
	prevLessons, _ := s.journal.LoadLessons(s.config.Journal.MaxLessonAgeDays, s.config.Journal.MaxLessonsInPrompt)
	var prevLessonLines []string
	for _, l := range prevLessons {
		prevLessonLines = append(prevLessonLines, fmt.Sprintf("[%s] %s: %s → %s", l.Confidence, l.Pattern, l.Observation, l.Recommendation))
	}

	reviewReq := &ai.JournalReviewRequest{
		Outcomes: journalOutcomes,
		Stats: ai.JournalReviewStats{
			TotalTrades:      total,
			WinRate:          winRate,
			AvgWinPnL:        safeDiv(winPnL, float64(wins)),
			AvgLossPnL:       safeDiv(lossPnL, float64(losses)),
			AvgHoldHoursWin:  safeDiv(winHours, float64(wins)),
			AvgHoldHoursLoss: safeDiv(lossHours, float64(losses)),
			ByExitReason:     exitReasons,
		},
		PreviousLessons: prevLessonLines,
		CurrentDate:     time.Now().In(s.loc),
	}

	rawLessons, rawResponse, err := s.ai.JournalReview(ctx, reviewReq)
	if err != nil {
		s.logger.Error("daily review: LLM call", "error", err)
		return
	}

	// Convert raw lessons to journal.Lesson
	var lessons []journal.Lesson
	for _, raw := range rawLessons {
		l := journal.Lesson{}
		if v, ok := raw["pattern"].(string); ok {
			l.Pattern = v
		}
		if v, ok := raw["observation"].(string); ok {
			l.Observation = v
		}
		if v, ok := raw["recommendation"].(string); ok {
			l.Recommendation = v
		}
		if v, ok := raw["confidence"].(string); ok {
			l.Confidence = v
		}
		if arr, ok := raw["tickers"].([]interface{}); ok {
			for _, t := range arr {
				if str, ok := t.(string); ok {
					l.Tickers = append(l.Tickers, str)
				}
			}
		}
		if l.Pattern != "" {
			lessons = append(lessons, l)
		}
	}

	prevDay := journal.PreviousTradingDay(time.Now().In(s.loc))
	if err := s.journal.SaveReview(prevDay.Format("2006-01-02"), lessons, rawResponse, len(outcomes)); err != nil {
		s.logger.Error("daily review: save", "error", err)
		return
	}

	s.logger.Info("daily review complete", "lessons", len(lessons), "trades_reviewed", len(outcomes))
	for _, l := range lessons {
		s.logger.Info("lesson", "confidence", l.Confidence, "pattern", l.Pattern, "recommendation", l.Recommendation)
	}
}
