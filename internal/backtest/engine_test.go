package backtest

import (
	"testing"
	"time"

	"github.com/camuig/rus-trader/internal/storage"
)

func TestCalcMaxDrawdown(t *testing.T) {
	trades := []TradeResult{
		{PnL: 100, TestAllowed: true},
		{PnL: -50, TestAllowed: true},
		{PnL: -80, TestAllowed: true},
		{PnL: 200, TestAllowed: true},
	}
	dd := calcMaxDrawdown(trades, func(tr TradeResult) bool { return tr.TestAllowed })
	// Cumulative: 100, 50, -30, 170. Peak=100, trough=-30, DD=130
	if dd < 129 || dd > 131 {
		t.Errorf("expected drawdown ~130, got %.0f", dd)
	}
}

func TestCalcMaxDrawdown_NoTrades(t *testing.T) {
	dd := calcMaxDrawdown(nil, func(tr TradeResult) bool { return true })
	if dd != 0 {
		t.Errorf("expected 0 for empty trades, got %.0f", dd)
	}
}

func TestBuildStats(t *testing.T) {
	trades := []TradeResult{
		{PnL: 100, TestAllowed: true},
		{PnL: -50, TestAllowed: true},
		{PnL: 200, TestAllowed: true},
		{PnL: -30, TestAllowed: false}, // filtered out
	}
	stats := buildStats(trades, func(tr TradeResult) bool { return tr.TestAllowed })
	if stats.Trades != 3 {
		t.Errorf("expected 3 trades, got %d", stats.Trades)
	}
	if stats.Wins != 2 {
		t.Errorf("expected 2 wins, got %d", stats.Wins)
	}
	if stats.TotalPnL != 250 {
		t.Errorf("expected PnL 250, got %.0f", stats.TotalPnL)
	}
}

func TestReconstructPairs(t *testing.T) {
	now := time.Now()
	trades := []storage.Trade{
		{Ticker: "SBER", Action: "BUY", Price: 300, Quantity: 10, StopLossPrice: 290, TakeProfitPrice: 320, EntryFeatures: "test", CreatedAt: now},
		{Ticker: "SBER", Action: "SELL", Price: 310, PnL: 100, CreatedAt: now.Add(time.Hour)},
		{Ticker: "GAZP", Action: "BUY", Price: 150, Quantity: 20, EntryFeatures: "аптренд, RSI 60, ATR 2.5%", CreatedAt: now},
		{Ticker: "GAZP", Action: "SELL", Price: 145, PnL: -100, CreatedAt: now.Add(2 * time.Hour)},
	}
	pairs := reconstructPairs(trades)
	if len(pairs) != 2 {
		t.Fatalf("expected 2 pairs, got %d", len(pairs))
	}
	if pairs[0].Ticker != "SBER" || pairs[0].PnL != 100 {
		t.Errorf("unexpected first pair: %+v", pairs[0])
	}
}

func TestReconstructPairs_UnmatchedSell(t *testing.T) {
	now := time.Now()
	trades := []storage.Trade{
		{Ticker: "SBER", Action: "SELL", Price: 310, PnL: 100, CreatedAt: now},
	}
	pairs := reconstructPairs(trades)
	if len(pairs) != 0 {
		t.Errorf("expected 0 pairs for unmatched SELL, got %d", len(pairs))
	}
}

func TestReconstructPairs_SLTPPct(t *testing.T) {
	now := time.Now()
	trades := []storage.Trade{
		{Ticker: "SBER", Action: "BUY", Price: 200, StopLossPrice: 190, TakeProfitPrice: 220, CreatedAt: now},
		{Ticker: "SBER", Action: "SELL", Price: 210, PnL: 50, CreatedAt: now.Add(time.Hour)},
	}
	pairs := reconstructPairs(trades)
	if len(pairs) != 1 {
		t.Fatalf("expected 1 pair, got %d", len(pairs))
	}
	// SL: (200-190)/200*100 = 5%
	if pairs[0].StopLossPct < 4.9 || pairs[0].StopLossPct > 5.1 {
		t.Errorf("expected SL ~5%%, got %.1f%%", pairs[0].StopLossPct)
	}
	// TP: (220-200)/200*100 = 10%
	if pairs[0].TakeProfitPct < 9.9 || pairs[0].TakeProfitPct > 10.1 {
		t.Errorf("expected TP ~10%%, got %.1f%%", pairs[0].TakeProfitPct)
	}
}

func TestBuildStats_Empty(t *testing.T) {
	stats := buildStats(nil, func(tr TradeResult) bool { return true })
	if stats.Trades != 0 || stats.TotalPnL != 0 {
		t.Errorf("expected zero stats, got %+v", stats)
	}
}

func TestBuildStats_WinRate(t *testing.T) {
	trades := []TradeResult{
		{PnL: 100, RealAllowed: true},
		{PnL: -50, RealAllowed: true},
		{PnL: 200, RealAllowed: true},
		{PnL: -10, RealAllowed: true},
	}
	stats := buildStats(trades, func(tr TradeResult) bool { return tr.RealAllowed })
	// 2 wins out of 4 = 50%
	if stats.WinRate < 49 || stats.WinRate > 51 {
		t.Errorf("expected WR ~50%%, got %.0f%%", stats.WinRate)
	}
	if stats.AvgWinPnL != 150 { // (100+200)/2
		t.Errorf("expected avg win 150, got %.0f", stats.AvgWinPnL)
	}
	if stats.AvgLossPnL != -30 { // (-50+-10)/2
		t.Errorf("expected avg loss -30, got %.0f", stats.AvgLossPnL)
	}
}
