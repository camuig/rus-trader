package scheduler

import (
	"context"
	"time"

	"github.com/camuig/rus-trader/internal/ai"
	"github.com/camuig/rus-trader/internal/broker"
	"github.com/camuig/rus-trader/internal/config"
	"github.com/camuig/rus-trader/internal/dividends"
	"github.com/camuig/rus-trader/internal/executor"
	"github.com/camuig/rus-trader/internal/guard"
	"github.com/camuig/rus-trader/internal/journal"
	"github.com/camuig/rus-trader/internal/logger"
	"github.com/camuig/rus-trader/internal/moex"
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

	// Инжектируемый lookup размера лота — используется в unit-тестах в обход gRPC-брокера.
	// Если nil, по умолчанию вызывается s.broker.GetLotSize.
	lotSizeFn func(uid string) (int32, error)
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

	go s.RunStopWatchdog(ctx)

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
