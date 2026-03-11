package guard

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/camuig/rus-trader/internal/ai"
	"github.com/camuig/rus-trader/internal/config"
	"github.com/camuig/rus-trader/internal/logger"
	"github.com/camuig/rus-trader/internal/storage"
)

func TestFilter_SellIsProcessedBeforeBuyAndFreesSlot(t *testing.T) {
	g, repo := newTestGuard(t, config.TradingConfig{
		MaxOpenPositions: 5,
		MaxDailyTrades:   100,
		CooldownMinutes:  120,
		MinHoldMinutes:   0,
	})

	for _, ticker := range []string{"SBER", "GAZP", "LKOH", "NVTK", "ROSN"} {
		saveTrade(t, repo, &storage.Trade{
			Ticker:    ticker,
			Action:    "BUY",
			Price:     100,
			Quantity:  1,
			Status:    "open",
			CreatedAt: time.Now().Add(-2 * time.Hour),
		})
	}

	allowed, blocked := g.Filter([]ai.AIDecision{
		{Action: "BUY", Ticker: "MOEX"},
		{Action: "SELL", Ticker: "SBER"},
	})

	if len(blocked) != 0 {
		t.Fatalf("expected no blocked decisions, got %d", len(blocked))
	}
	if len(allowed) != 2 {
		t.Fatalf("expected 2 allowed decisions, got %d", len(allowed))
	}

	if allowed[0].Decision.Action != "SELL" || allowed[0].Decision.Ticker != "SBER" {
		t.Fatalf("expected first allowed decision to be SELL SBER, got %s %s", allowed[0].Decision.Action, allowed[0].Decision.Ticker)
	}
	if allowed[1].Decision.Action != "BUY" || allowed[1].Decision.Ticker != "MOEX" {
		t.Fatalf("expected second allowed decision to be BUY MOEX, got %s %s", allowed[1].Decision.Action, allowed[1].Decision.Ticker)
	}
}

func TestFilter_RespectsMaxOpenPositionsWithinSameBatch(t *testing.T) {
	g, repo := newTestGuard(t, config.TradingConfig{
		MaxOpenPositions: 5,
		MaxDailyTrades:   100,
		CooldownMinutes:  120,
		MinHoldMinutes:   0,
	})

	for _, ticker := range []string{"SBER", "GAZP", "LKOH", "NVTK", "ROSN"} {
		saveTrade(t, repo, &storage.Trade{
			Ticker:    ticker,
			Action:    "BUY",
			Price:     100,
			Quantity:  1,
			Status:    "open",
			CreatedAt: time.Now().Add(-2 * time.Hour),
		})
	}

	allowed, blocked := g.Filter([]ai.AIDecision{
		{Action: "BUY", Ticker: "MOEX"},
		{Action: "BUY", Ticker: "YDEX"},
		{Action: "SELL", Ticker: "SBER"},
	})

	if len(allowed) != 2 {
		t.Fatalf("expected 2 allowed decisions, got %d", len(allowed))
	}
	if len(blocked) != 1 {
		t.Fatalf("expected 1 blocked decision, got %d", len(blocked))
	}

	if allowed[0].Decision.Action != "SELL" || allowed[0].Decision.Ticker != "SBER" {
		t.Fatalf("expected first allowed decision to be SELL SBER, got %s %s", allowed[0].Decision.Action, allowed[0].Decision.Ticker)
	}
	if allowed[1].Decision.Action != "BUY" || allowed[1].Decision.Ticker != "MOEX" {
		t.Fatalf("expected second allowed decision to be BUY MOEX, got %s %s", allowed[1].Decision.Action, allowed[1].Decision.Ticker)
	}
	if blocked[0].Decision.Action != "BUY" || blocked[0].Decision.Ticker != "YDEX" {
		t.Fatalf("expected blocked decision to be BUY YDEX, got %s %s", blocked[0].Decision.Action, blocked[0].Decision.Ticker)
	}
	if !strings.Contains(blocked[0].Reason, "лимит открытых позиций") {
		t.Fatalf("expected max open positions block reason, got %q", blocked[0].Reason)
	}
}

func TestFilter_RespectsDailyBuyLimitWithinSameBatch(t *testing.T) {
	g, repo := newTestGuard(t, config.TradingConfig{
		MaxOpenPositions: 10,
		MaxDailyTrades:   2,
		CooldownMinutes:  120,
		MinHoldMinutes:   0,
	})

	saveTrade(t, repo, &storage.Trade{
		Ticker:    "SBER",
		Action:    "BUY",
		Price:     100,
		Quantity:  1,
		Status:    "closed",
		CreatedAt: time.Now().Add(-10 * time.Minute),
	})

	allowed, blocked := g.Filter([]ai.AIDecision{
		{Action: "BUY", Ticker: "MOEX"},
		{Action: "BUY", Ticker: "YDEX"},
	})

	if len(allowed) != 1 {
		t.Fatalf("expected 1 allowed decision, got %d", len(allowed))
	}
	if len(blocked) != 1 {
		t.Fatalf("expected 1 blocked decision, got %d", len(blocked))
	}
	if allowed[0].Decision.Ticker != "MOEX" {
		t.Fatalf("expected BUY MOEX to be allowed first, got %s", allowed[0].Decision.Ticker)
	}
	if blocked[0].Decision.Ticker != "YDEX" {
		t.Fatalf("expected BUY YDEX to be blocked second, got %s", blocked[0].Decision.Ticker)
	}
	if !strings.Contains(blocked[0].Reason, "лимит сделок за день") {
		t.Fatalf("expected daily trades limit block reason, got %q", blocked[0].Reason)
	}
}

func TestFilter_DuplicateSellDoesNotFreeTwoSlots(t *testing.T) {
	g, repo := newTestGuard(t, config.TradingConfig{
		MaxOpenPositions: 5,
		MaxDailyTrades:   100,
		CooldownMinutes:  120,
		MinHoldMinutes:   0,
	})

	for _, ticker := range []string{"SBER", "GAZP", "LKOH", "NVTK", "ROSN"} {
		saveTrade(t, repo, &storage.Trade{
			Ticker:    ticker,
			Action:    "BUY",
			Price:     100,
			Quantity:  1,
			Status:    "open",
			CreatedAt: time.Now().Add(-2 * time.Hour),
		})
	}

	allowed, blocked := g.Filter([]ai.AIDecision{
		{Action: "SELL", Ticker: "SBER"},
		{Action: "SELL", Ticker: "SBER"},
		{Action: "BUY", Ticker: "MOEX"},
		{Action: "BUY", Ticker: "YDEX"},
	})

	if len(allowed) != 2 {
		t.Fatalf("expected 2 allowed decisions, got %d", len(allowed))
	}
	if len(blocked) != 2 {
		t.Fatalf("expected 2 blocked decisions, got %d", len(blocked))
	}

	if allowed[0].Decision.Action != "SELL" || allowed[0].Decision.Ticker != "SBER" {
		t.Fatalf("expected first allowed decision to be SELL SBER, got %s %s", allowed[0].Decision.Action, allowed[0].Decision.Ticker)
	}
	if allowed[1].Decision.Action != "BUY" || allowed[1].Decision.Ticker != "MOEX" {
		t.Fatalf("expected second allowed decision to be BUY MOEX, got %s %s", allowed[1].Decision.Action, allowed[1].Decision.Ticker)
	}

	if blocked[0].Decision.Action != "SELL" || blocked[0].Decision.Ticker != "SBER" {
		t.Fatalf("expected first blocked decision to be duplicate SELL SBER, got %s %s", blocked[0].Decision.Action, blocked[0].Decision.Ticker)
	}
	if !strings.Contains(blocked[0].Reason, "уже закрывается") {
		t.Fatalf("expected duplicate sell block reason, got %q", blocked[0].Reason)
	}

	if blocked[1].Decision.Action != "BUY" || blocked[1].Decision.Ticker != "YDEX" {
		t.Fatalf("expected second blocked decision to be BUY YDEX, got %s %s", blocked[1].Decision.Action, blocked[1].Decision.Ticker)
	}
	if !strings.Contains(blocked[1].Reason, "лимит открытых позиций") {
		t.Fatalf("expected max open positions block reason, got %q", blocked[1].Reason)
	}
}

func newTestGuard(t *testing.T, trading config.TradingConfig) (*TradeGuard, *storage.Repository) {
	t.Helper()

	dbPath := filepath.Join(t.TempDir(), "guard-test.db")
	db, err := storage.NewDatabase(dbPath)
	if err != nil {
		t.Fatalf("create test database: %v", err)
	}

	repo := storage.NewRepository(db)
	cfg := &config.Config{
		Trading: trading,
	}
	return NewTradeGuard(repo, cfg, logger.New("error")), repo
}

func saveTrade(t *testing.T, repo *storage.Repository, trade *storage.Trade) {
	t.Helper()
	if err := repo.SaveTrade(trade); err != nil {
		t.Fatalf("save trade %s %s: %v", trade.Action, trade.Ticker, err)
	}
}
