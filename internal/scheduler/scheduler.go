package scheduler

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/camuig/rus-trader/internal/ai"
	"github.com/camuig/rus-trader/internal/broker"
	"github.com/camuig/rus-trader/internal/config"
	"github.com/camuig/rus-trader/internal/executor"
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
		config:   cfg,
		logger:   log,
		loc:      cfg.MOEXLocation(),
	}
}

func (s *Scheduler) Run(ctx context.Context) {
	interval := s.config.TradingInterval()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	s.logger.Info("scheduler started", "interval", interval.String())

	// Run immediately on start
	s.runCycle(ctx)

	for {
		select {
		case <-ctx.Done():
			s.logger.Info("scheduler stopped")
			return
		case <-ticker.C:
			s.runCycle(ctx)
		}
	}
}

func (s *Scheduler) runCycle(ctx context.Context) {
	defer func() {
		if r := recover(); r != nil {
			s.logger.Error("panic in scheduler cycle", "panic", fmt.Sprint(r))
			s.notifier.NotifyError("scheduler panic", fmt.Errorf("%v", r))
		}
	}()

	if !s.isWithinTradingHours() {
		s.logger.Info("outside trading hours, skipping cycle")
		return
	}

	s.logger.Info("starting analysis cycle")

	// 1. Fetch top tickers from MOEX
	topTickers, err := s.moex.FetchTopTickers(ctx, 50)
	if err != nil {
		s.logger.Error("fetch top tickers", "error", err)
		s.saveAnalysisLog(0, "", "", err)
		return
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
		return
	}

	var tradableTickers []string
	for _, uid := range uids {
		if tradable[uid] {
			tradableTickers = append(tradableTickers, uidToTicker[uid])
		}
	}
	s.logger.Info("tradable tickers", "count", len(tradableTickers))

	if len(tradableTickers) == 0 {
		s.logger.Info("no tradable tickers, skipping cycle")
		return
	}

	// 3. Fetch candle snapshots
	concurrency := s.config.Trading.CandleConcurrency
	snapshots := s.broker.FetchCandleSnapshots(tradableTickers, concurrency)
	s.logger.Info("candle snapshots fetched", "count", len(snapshots))

	// 4. Fetch news and filter by tickers
	allNews, err := s.moex.FetchRecentNews(ctx)
	if err != nil {
		s.logger.Error("fetch news", "error", err)
		// non-fatal, continue without news
		allNews = nil
	}
	tickerNews := moex.FilterNewsForTickers(allNews, tradableTickers)

	// 5. Get portfolio
	portfolio, err := s.broker.GetPortfolio()
	if err != nil {
		s.logger.Error("get portfolio", "error", err)
		s.saveAnalysisLog(len(tradableTickers), "", "", err)
		return
	}

	// 6. Build TickerAnalysis with percent changes and news
	tickerAnalyses := make([]ai.TickerAnalysis, 0, len(snapshots))
	for _, snap := range snapshots {
		ta := ai.TickerAnalysis{
			Ticker:     snap.Ticker,
			LastPrice:  snap.LastPrice,
			Price3hAgo: snap.Price3hAgo,
			Price1dAgo: snap.Price1dAgo,
			Price3dAgo: snap.Price3dAgo,
			Price1wAgo: snap.Price1wAgo,
			Volume24h:  snap.Volume24h,
			Change3h:   pctChange(snap.Price3hAgo, snap.LastPrice),
			Change1d:   pctChange(snap.Price1dAgo, snap.LastPrice),
			Change3d:   pctChange(snap.Price3dAgo, snap.LastPrice),
			Change1w:   pctChange(snap.Price1wAgo, snap.LastPrice),
		}

		if items, ok := tickerNews[snap.Ticker]; ok {
			for _, n := range items {
				ta.News = append(ta.News, n.Title)
			}
		}

		tickerAnalyses = append(tickerAnalyses, ta)
	}

	// 7. AI analysis
	analysisReq := &ai.AnalysisRequest{
		Tickers:      tickerAnalyses,
		Positions:    portfolio.Positions,
		AvailableRub: portfolio.AvailableRub,
		TotalRub:     portfolio.TotalRub,
	}

	decisions, rawResponse, err := s.ai.Analyze(ctx, analysisReq)
	if err != nil {
		s.logger.Error("AI analysis", "error", err)
		s.saveAnalysisLog(len(tradableTickers), rawResponse, "", err)
		return
	}

	s.logger.Info("AI decisions received", "count", len(decisions))
	for _, d := range decisions {
		s.logger.Debug("AI decision",
			"action", d.Action, "ticker", d.Ticker,
			"confidence", d.Confidence, "stop_loss", d.StopLoss,
			"take_profit", d.TakeProfit, "reasoning", d.Reasoning)
	}

	// 8. Execute decisions
	s.executor.Execute(decisions)

	// 9. Save analysis log and portfolio snapshot
	s.saveAnalysisLog(len(tradableTickers), rawResponse, executor.DecisionsToJSON(decisions), nil)
	s.savePortfolioSnapshot(portfolio)

	s.logger.Info("analysis cycle completed")
}

func pctChange(from, to float64) float64 {
	if from == 0 {
		return 0
	}
	return (to - from) / from * 100
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
