package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
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
	"github.com/camuig/rus-trader/internal/scheduler"
	"github.com/camuig/rus-trader/internal/sentiment"
	"github.com/camuig/rus-trader/internal/storage"
	"github.com/camuig/rus-trader/internal/telegram"
	"github.com/camuig/rus-trader/internal/vectordb"
	"github.com/camuig/rus-trader/internal/web"
)

func main() {
	configPath := flag.String("config", "config.yaml", "path to config file")
	dbPath := flag.String("db", "data/rus-trader.db", "path to SQLite database")
	flag.Parse()

	// Load config
	cfg, err := config.Load(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "config error: %v\n", err)
		os.Exit(1)
	}

	// Init logger
	log := logger.New(cfg.Logging.Level)

	mode := "LIVE"
	if cfg.IsSandbox() {
		mode = "SANDBOX"
	}
	log.Info("starting rus-trader", "mode", mode)

	// Init database
	db, err := storage.NewDatabase(*dbPath)
	if err != nil {
		log.Error("database init failed", "error", err)
		os.Exit(1)
	}
	repo := storage.NewRepository(db)

	// Context with cancellation
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Init broker client
	bc, err := broker.NewBrokerClient(ctx, cfg, log)
	if err != nil {
		log.Error("broker client init failed", "error", err)
		os.Exit(1)
	}
	log.Info("broker connected", "account_id", bc.AccountID())

	// Init services
	aiClient := ai.NewClient(cfg, log)
	notifier := telegram.NewNotifier(cfg, log)
	exec := executor.NewExecutor(bc, repo, notifier, cfg, log)
	moexClient := moex.NewClient(log)
	tradeGuard := guard.NewTradeGuard(repo, cfg, log)
	tradeGuard.SetLotSizeFn(func(ticker string) int64 {
		uid, err := bc.ResolveTickerToUID(ticker)
		if err != nil {
			return 1
		}
		lotSize, err := bc.GetLotSize(uid)
		if err != nil || lotSize <= 0 {
			return 1
		}
		return int64(lotSize)
	})

	// Dividend fetcher (optional)
	var divFetcher *dividends.Fetcher
	if cfg.Dividends.Enabled {
		divFetcher = dividends.NewFetcher(
			time.Duration(cfg.Dividends.CacheTTLHours)*time.Hour,
			log,
		)
	}

	// Trade journal (optional)
	var j *journal.Journal
	if cfg.Journal.Enabled {
		j = journal.New(repo, log)
		exec.SetJournal(j)
	}

	// Sentiment scorer (optional)
	var sentScorer *sentiment.Scorer
	if cfg.Sentiment.Enabled {
		sentScorer = sentiment.NewScorer(aiClient, cfg, log)
	}

	// Vector pattern store (optional)
	var vs *vectordb.Store
	if cfg.VectorDB.Enabled && cfg.VectorDB.OpenRouterAPIKey != "" {
		embedder := vectordb.NewEmbeddingClient(cfg.VectorDB.OpenRouterAPIKey, cfg.VectorDB.EmbeddingModel, log)
		vs = vectordb.NewStore(repo, embedder, log)
		if err := vs.LoadAll(); err != nil {
			log.Error("vector store load failed", "error", err)
		}
		exec.SetVectorStore(vs)
	}

	sched := scheduler.NewScheduler(bc, moexClient, aiClient, exec, repo, notifier, tradeGuard, cfg, log, divFetcher, j, sentScorer, vs)
	webServer := web.NewServer(bc, repo, cfg, log, moexClient)

	// Start scheduler in goroutine
	go sched.Run(ctx)

	// Start web server in goroutine
	go func() {
		if err := webServer.Start(); err != nil {
			log.Error("web server error", "error", err)
		}
	}()

	notifier.NotifyStatus(fmt.Sprintf("🤖 Rus-Trader запущен (%s)", mode))

	// Wait for shutdown signal
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	sig := <-sigCh
	log.Info("shutdown signal received", "signal", sig.String())

	// Graceful shutdown
	cancel() // stop scheduler

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*1e9)
	defer shutdownCancel()

	if err := webServer.Shutdown(shutdownCtx); err != nil {
		log.Error("web server shutdown error", "error", err)
	}

	if err := bc.Stop(); err != nil {
		log.Error("broker client stop error", "error", err)
	}

	notifier.NotifyStatus("🛑 Rus-Trader остановлен")
	log.Info("rus-trader stopped")
}
