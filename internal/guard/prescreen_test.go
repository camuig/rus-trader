package guard

import (
	"strings"
	"testing"
	"time"

	"github.com/camuig/rus-trader/internal/broker"
	"github.com/camuig/rus-trader/internal/config"
	"github.com/camuig/rus-trader/internal/indicators"
	"github.com/camuig/rus-trader/internal/storage"
)

func TestPreScreenBuyable_BlocksByATR(t *testing.T) {
	g, _ := newTestGuard(t, config.TradingConfig{
		MaxOpenPositions: 5,
		MaxDailyTrades:   100,
		CooldownMinutes:  0,
		MinATRPct:        1.2,
	})

	snapshots := []broker.CandleSnapshot{
		{
			Ticker: "LOWVOL",
			Indicators: indicators.Indicators{
				RSI14: 50, EMA9: 100, EMA21: 100, ATR14: 0.5, // 0.5% ATR
			},
		},
		{
			Ticker: "GOOD",
			Indicators: indicators.Indicators{
				RSI14: 50, EMA9: 105, EMA21: 100, ATR14: 3.0, // 3% ATR
			},
		},
	}

	res := g.PreScreenBuyable(snapshots, nil)

	if len(res.Allowed) != 1 || res.Allowed[0].Ticker != "GOOD" {
		t.Fatalf("expected only GOOD to pass, got %+v", tickersOf(res.Allowed))
	}
	if reason, ok := res.Blocked["LOWVOL"]; !ok || !strings.Contains(reason, "ATR") {
		t.Fatalf("expected LOWVOL to be blocked by ATR, got %q", reason)
	}
}

func TestPreScreenBuyable_BlocksByRSIOverbought(t *testing.T) {
	g, _ := newTestGuard(t, config.TradingConfig{
		MaxOpenPositions: 5,
		MaxDailyTrades:   100,
	})

	snapshots := []broker.CandleSnapshot{
		{
			Ticker: "HOT",
			Indicators: indicators.Indicators{
				RSI14: 85, EMA9: 105, EMA21: 100, ATR14: 3.0,
			},
		},
	}

	res := g.PreScreenBuyable(snapshots, nil)

	if len(res.Allowed) != 0 {
		t.Fatalf("expected HOT to be blocked by RSI, got allowed=%v", tickersOf(res.Allowed))
	}
	if reason := res.Blocked["HOT"]; !strings.Contains(reason, "RSI") {
		t.Fatalf("expected RSI block reason, got %q", reason)
	}
}

func TestPreScreenBuyable_BlocksByUptrendRequirement(t *testing.T) {
	g, _ := newTestGuard(t, config.TradingConfig{
		MaxOpenPositions: 5,
		MaxDailyTrades:   100,
		RequireUptrend:   true,
	})

	snapshots := []broker.CandleSnapshot{
		{
			Ticker: "DOWN",
			Indicators: indicators.Indicators{
				RSI14: 50, EMA9: 95, EMA21: 100, ATR14: 3.0,
			},
		},
	}

	res := g.PreScreenBuyable(snapshots, nil)

	if len(res.Allowed) != 0 {
		t.Fatalf("expected DOWN to be blocked by downtrend, got allowed=%v", tickersOf(res.Allowed))
	}
	if reason := res.Blocked["DOWN"]; !strings.Contains(reason, "тренд") {
		t.Fatalf("expected downtrend block reason, got %q", reason)
	}
}

func TestPreScreenBuyable_AlwaysAllowsPositionTickers(t *testing.T) {
	g, _ := newTestGuard(t, config.TradingConfig{
		MaxOpenPositions: 5,
		MaxDailyTrades:   100,
		MinATRPct:        1.2,
		RequireUptrend:   true,
	})

	// Тикер с очень плохими индикаторами, но у нас открытая позиция.
	snapshots := []broker.CandleSnapshot{
		{
			Ticker: "HELD",
			Indicators: indicators.Indicators{
				RSI14: 90, EMA9: 90, EMA21: 100, ATR14: 0.1,
			},
		},
	}
	positions := map[string]bool{"HELD": true}

	res := g.PreScreenBuyable(snapshots, positions)

	if len(res.Allowed) != 1 || res.Allowed[0].Ticker != "HELD" {
		t.Fatalf("expected HELD to pass (position ticker), got %+v", tickersOf(res.Allowed))
	}
	if _, blocked := res.Blocked["HELD"]; blocked {
		t.Fatalf("position ticker must not be in Blocked")
	}
}

func TestPreScreenBuyable_BlocksByCooldownAfterSell(t *testing.T) {
	g, repo := newTestGuard(t, config.TradingConfig{
		MaxOpenPositions: 5,
		MaxDailyTrades:   100,
		CooldownMinutes:  120,
	})

	saveTrade(t, repo, &storage.Trade{
		Ticker:    "JUST_SOLD",
		Action:    "SELL",
		Price:     100,
		Quantity:  1,
		Status:    "closed",
		CreatedAt: time.Now().Add(-30 * time.Minute),
	})

	snapshots := []broker.CandleSnapshot{
		{
			Ticker: "JUST_SOLD",
			Indicators: indicators.Indicators{
				RSI14: 50, EMA9: 105, EMA21: 100, ATR14: 3.0,
			},
		},
	}

	res := g.PreScreenBuyable(snapshots, nil)

	if len(res.Allowed) != 0 {
		t.Fatalf("expected JUST_SOLD to be blocked by cooldown, got %+v", tickersOf(res.Allowed))
	}
	if reason := res.Blocked["JUST_SOLD"]; !strings.Contains(reason, "cooldown") {
		t.Fatalf("expected cooldown block reason, got %q", reason)
	}
}

func TestCanOpenNewPositions_MaxOpenExceeded(t *testing.T) {
	g, repo := newTestGuard(t, config.TradingConfig{
		MaxOpenPositions: 2,
		MaxDailyTrades:   100,
	})

	for _, ticker := range []string{"A", "B"} {
		saveTrade(t, repo, &storage.Trade{
			Ticker:    ticker,
			Action:    "BUY",
			Price:     100,
			Quantity:  1,
			Status:    "open",
			CreatedAt: time.Now().Add(-2 * time.Hour),
		})
	}

	ok, reason := g.CanOpenNewPositions()
	if ok {
		t.Fatalf("expected CanOpenNewPositions=false when max_open_positions reached")
	}
	if !strings.Contains(reason, "лимит открытых позиций") {
		t.Fatalf("unexpected reason: %q", reason)
	}
}

func TestCanOpenNewPositions_DailyTradesExceeded(t *testing.T) {
	g, repo := newTestGuard(t, config.TradingConfig{
		MaxOpenPositions: 10,
		MaxDailyTrades:   1,
	})

	saveTrade(t, repo, &storage.Trade{
		Ticker:    "A",
		Action:    "BUY",
		Price:     100,
		Quantity:  1,
		Status:    "closed",
		CreatedAt: time.Now().Add(-1 * time.Hour),
	})

	ok, reason := g.CanOpenNewPositions()
	if ok {
		t.Fatalf("expected CanOpenNewPositions=false when daily trades reached")
	}
	if !strings.Contains(reason, "лимит сделок за день") {
		t.Fatalf("unexpected reason: %q", reason)
	}
}

func TestCanOpenNewPositions_Ok(t *testing.T) {
	g, _ := newTestGuard(t, config.TradingConfig{
		MaxOpenPositions: 5,
		MaxDailyTrades:   100,
	})

	ok, reason := g.CanOpenNewPositions()
	if !ok {
		t.Fatalf("expected CanOpenNewPositions=true on empty state, got reason=%q", reason)
	}
}

func tickersOf(snaps []broker.CandleSnapshot) []string {
	out := make([]string, 0, len(snaps))
	for _, s := range snaps {
		out = append(out, s.Ticker)
	}
	return out
}
