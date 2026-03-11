package scheduler

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/camuig/rus-trader/internal/ai"
	"github.com/camuig/rus-trader/internal/broker"
	"github.com/camuig/rus-trader/internal/config"
	"github.com/camuig/rus-trader/internal/executor"
	"github.com/camuig/rus-trader/internal/guard"
	"github.com/camuig/rus-trader/internal/logger"
	"github.com/camuig/rus-trader/internal/moex"
	"github.com/camuig/rus-trader/internal/storage"
	"github.com/camuig/rus-trader/internal/telegram"
)

type Scheduler struct {
	broker   *broker.BrokerClient
	moex     *moex.Client
	ai       *ai.DeepSeekClient
	executor *executor.Executor
	repo     *storage.Repository
	notifier *telegram.Notifier
	guard    *guard.TradeGuard
	config   *config.Config
	logger   *logger.Logger
	loc      *time.Location
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
) *Scheduler {
	return &Scheduler{
		broker:   bc,
		moex:     moexClient,
		ai:       aiClient,
		executor: exec,
		repo:     repo,
		notifier: notifier,
		guard:    g,
		config:   cfg,
		logger:   log,
		loc:      cfg.MOEXLocation(),
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
	snapshots := s.broker.FetchCandleSnapshots(tradableTickers, concurrency)
	s.logger.Info("candle snapshots fetched", "count", len(snapshots))

	// 6. Fetch ticker briefs (cached, non-fatal)
	tickerBriefs := s.fetchTickerBriefs(tradableTickers)

	// 6. Fetch news and filter by tickers
	allNews, err := s.moex.FetchRecentNews(ctx)
	if err != nil {
		s.logger.Error("fetch news", "error", err)
		// non-fatal, continue without news
		allNews = nil
	}
	tickerNews := moex.FilterNewsForTickers(allNews, tradableTickers)

	// 7. Fetch world news (non-fatal)
	worldCtx, cancelWorld := context.WithTimeout(ctx, 8*time.Second)
	worldNewsItems, err := s.moex.FetchWorldNews(worldCtx, s.config.DeepSeek.MaxWorldNewsItems*2)
	cancelWorld()
	if err != nil {
		s.logger.Error("fetch world news", "error", err)
		worldNewsItems = nil
	}
	globalNews := make([]string, 0, len(worldNewsItems))
	for _, n := range worldNewsItems {
		if n.Source != "" {
			globalNews = append(globalNews, fmt.Sprintf("%s: %s", n.Source, n.Title))
		} else {
			globalNews = append(globalNews, n.Title)
		}
	}
	s.logger.Info("world news fetched", "count", len(globalNews))

	// 8. Build TickerAnalysis with OHLCV data and news
	tickerAnalyses := make([]ai.TickerAnalysis, 0, len(snapshots))
	for _, snap := range snapshots {
		ta := ai.TickerAnalysis{
			Ticker:    snap.Ticker,
			Brief:     tickerBriefs[snap.Ticker],
			LastPrice: snap.LastPrice,
			Period3h:  toPeriodData(snap.Period3h),
			Period1d:  toPeriodData(snap.Period1d),
			Period3d:  toPeriodData(snap.Period3d),
			Period1w:  toPeriodData(snap.Period1w),
		}

		if items, ok := tickerNews[snap.Ticker]; ok {
			for _, n := range items {
				ta.News = append(ta.News, n.Title)
			}
		}

		tickerAnalyses = append(tickerAnalyses, ta)
	}

	// 9. Fetch recent closed trades for AI context
	var recentTrades []ai.RecentClosedTrade
	if closedTrades, err := s.repo.GetClosedTradesLast24h(); err == nil {
		for _, t := range closedTrades {
			entryPrice := t.Price // exit price from SELL record
			if t.Quantity > 0 {
				entryPrice = t.Price - t.PnL/float64(t.Quantity)
			}
			recentTrades = append(recentTrades, ai.RecentClosedTrade{
				Ticker:     t.Ticker,
				EntryPrice: entryPrice,
				ExitPrice:  t.Price,
				Quantity:   t.Quantity,
				PnL:        t.PnL,
				ClosedAt:   t.CreatedAt,
				Reasoning:  t.Reasoning,
			})
		}
	}

	// 10. Fetch open trade context for AI (including SL/TP plan)
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

	// 11. Fetch today's traded tickers for anti-churning
	todayTraded, _ := s.repo.GetTodayTradedTickers()

	// 12. AI analysis
	analysisReq := &ai.AnalysisRequest{
		Tickers:      tickerAnalyses,
		GlobalNews:   globalNews,
		Positions:    portfolio.Positions,
		RecentTrades: recentTrades,
		OpenContext:  openContext,
		AvailableRub: portfolio.AvailableRub,
		TotalRub:     portfolio.TotalRub,
	}

	decisions, rawResponse, err := s.ai.Analyze(ctx, analysisReq, todayTraded)
	if err != nil {
		s.logger.Error("AI analysis", "error", err)
		s.saveAnalysisLog(len(tradableTickers), rawResponse, "", err)
		return false
	}

	s.logger.Info("AI decisions received", "count", len(decisions))
	for _, d := range decisions {
		s.logger.Info("AI decision",
			"action", d.Action, "ticker", d.Ticker,
			"confidence", d.Confidence, "reasoning", d.Reasoning)
	}

	// 13. Apply TradeGuard filter
	allowed, blocked := s.guard.Filter(decisions)
	for _, b := range blocked {
		s.notifier.NotifyBlocked(b.Decision.Ticker, b.Decision.Action, b.Reason)
	}

	allowedDecisions := make([]ai.AIDecision, len(allowed))
	for i, a := range allowed {
		allowedDecisions[i] = a.Decision
	}
	s.logger.Info("guard filter applied",
		"allowed", len(allowedDecisions), "blocked", len(blocked))

	// 14. Execute decisions
	s.executor.Execute(allowedDecisions)

	// 12. Save analysis log and portfolio snapshot
	s.saveAnalysisLog(len(tradableTickers), rawResponse, executor.DecisionsToJSON(decisions), nil)
	s.savePortfolioSnapshot(portfolio)

	s.logger.Info("analysis cycle completed")
	return true
}

func toPeriodData(p broker.PeriodOHLCV) ai.PeriodData {
	var changePct float64
	if p.Open > 0 {
		changePct = (p.Close - p.Open) / p.Open * 100
	}
	return ai.PeriodData{
		Open:      p.Open,
		High:      p.High,
		Low:       p.Low,
		Close:     p.Close,
		Volume:    p.Volume,
		ChangePct: changePct,
	}
}

func (s *Scheduler) isWithinTradingHours() bool {
	now := time.Now().In(s.loc)

	// Skip weekends
	weekday := now.Weekday()
	if weekday == time.Saturday || weekday == time.Sunday {
		return false
	}

	hour := now.Hour()
	minute := now.Minute()
	totalMinutes := hour*60 + minute

	// MOEX main session: 10:00 - 18:50 MSK
	return totalMinutes >= 600 && totalMinutes <= 1130
}

func (s *Scheduler) saveAnalysisLog(tickersCount int, rawResponse, decisionsJSON string, err error) {
	log := &storage.AnalysisLog{
		SignalsCount:  tickersCount,
		AIResponse:    rawResponse,
		DecisionsJSON: decisionsJSON,
	}
	if err != nil {
		log.Error = err.Error()
	}
	if dbErr := s.repo.SaveAnalysisLog(log); dbErr != nil {
		s.logger.Error("save analysis log", "error", dbErr)
	}
}

func (s *Scheduler) savePortfolioSnapshot(portfolio *broker.PortfolioInfo) {
	positionsJSON, _ := json.Marshal(portfolio.Positions)
	snapshot := &storage.PortfolioSnapshot{
		TotalRub:       portfolio.TotalRub,
		AvailableRub:   portfolio.AvailableRub,
		PositionsCount: len(portfolio.Positions),
		PositionsJSON:  string(positionsJSON),
	}
	if err := s.repo.SavePortfolioSnapshot(snapshot); err != nil {
		s.logger.Error("save portfolio snapshot", "error", err)
	}
}

func (s *Scheduler) fetchTickerBriefs(tickers []string) map[string]string {
	if len(tickers) == 0 {
		return nil
	}

	result := make(map[string]string, len(tickers))
	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, 5)

	for _, ticker := range tickers {
		t := ticker
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			brief, err := s.broker.GetTickerBrief(t)
			if err != nil {
				s.logger.Debug("fetch ticker brief failed", "ticker", t, "error", err)
				return
			}
			if brief == "" {
				return
			}

			mu.Lock()
			result[t] = brief
			mu.Unlock()
		}()
	}

	wg.Wait()
	s.logger.Info("ticker briefs fetched", "count", len(result))
	return result
}
