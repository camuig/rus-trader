package scheduler

import (
	"strings"
	"testing"

	"github.com/camuig/rus-trader/internal/broker"
	"github.com/camuig/rus-trader/internal/storage"
)

func TestSnapshotAnomaly(t *testing.T) {
	cases := []struct {
		name        string
		snap        broker.CandleSnapshot
		wantAnomaly bool
	}{
		{"normal day move", broker.CandleSnapshot{LastPrice: 98, Period1d: broker.PeriodOHLCV{Open: 100}}, false},
		{"crash beyond threshold", broker.CandleSnapshot{LastPrice: 0.23, Period1d: broker.PeriodOHLCV{Open: 29.85}}, true},
		{"pump beyond threshold", broker.CandleSnapshot{LastPrice: 200, Period1d: broker.PeriodOHLCV{Open: 100}}, true},
		{"zero last price", broker.CandleSnapshot{LastPrice: 0, Period1d: broker.PeriodOHLCV{Open: 100}}, true},
		{"no day open available", broker.CandleSnapshot{LastPrice: 100}, false},
		{"exactly at threshold", broker.CandleSnapshot{LastPrice: 70, Period1d: broker.PeriodOHLCV{Open: 100}}, false},
	}
	for _, c := range cases {
		got := snapshotAnomaly(c.snap, 30)
		if (got != "") != c.wantAnomaly {
			t.Errorf("%s: snapshotAnomaly = %q, wantAnomaly=%v", c.name, got, c.wantAnomaly)
		}
	}
}

func TestWatchdogAction_StopLossHit(t *testing.T) {
	trade := &storage.Trade{Ticker: "SBER", Price: 100, StopLossPrice: 97, TakeProfitPrice: 105}

	reason, ok := watchdogAction(trade, 96.5, 30)

	if !ok {
		t.Fatal("expected close on SL breach")
	}
	if !strings.Contains(reason, "SL") {
		t.Fatalf("reason = %q, want SL mention", reason)
	}
}

func TestWatchdogAction_TakeProfitHit(t *testing.T) {
	trade := &storage.Trade{Ticker: "SBER", Price: 100, StopLossPrice: 97, TakeProfitPrice: 105}

	reason, ok := watchdogAction(trade, 105.5, 30)

	if !ok {
		t.Fatal("expected close on TP reach")
	}
	if !strings.Contains(reason, "TP") {
		t.Fatalf("reason = %q, want TP mention", reason)
	}
}

func TestWatchdogAction_PriceBetweenSLAndTP(t *testing.T) {
	trade := &storage.Trade{Ticker: "SBER", Price: 100, StopLossPrice: 97, TakeProfitPrice: 105}

	if reason, ok := watchdogAction(trade, 101, 30); ok || reason != "" {
		t.Fatalf("expected no action, got ok=%v reason=%q", ok, reason)
	}
}

func TestWatchdogAction_BadQuoteSkipped(t *testing.T) {
	// Кейс EUTR: котировка 0.23 при входе 29.85 — битые данные, закрывать нельзя.
	trade := &storage.Trade{Ticker: "EUTR", Price: 29.85, StopLossPrice: 28.9}

	reason, ok := watchdogAction(trade, 0.23, 30)

	if ok {
		t.Fatal("bad quote must not trigger close")
	}
	if reason == "" {
		t.Fatal("expected anomaly description for logging")
	}
}

func TestWatchdogAction_ZeroPriceIgnored(t *testing.T) {
	trade := &storage.Trade{Ticker: "SBER", Price: 100, StopLossPrice: 97}

	if reason, ok := watchdogAction(trade, 0, 30); ok || reason != "" {
		t.Fatalf("expected silent skip on zero price, got ok=%v reason=%q", ok, reason)
	}
}

func TestWatchdogAction_NoStopLossSet(t *testing.T) {
	trade := &storage.Trade{Ticker: "SBER", Price: 100}

	if _, ok := watchdogAction(trade, 50, 0); ok {
		t.Fatal("no SL/TP set: watchdog must not close anything")
	}
}
