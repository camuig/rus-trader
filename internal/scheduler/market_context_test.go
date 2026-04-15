package scheduler

import (
	"testing"

	"github.com/camuig/rus-trader/internal/broker"
	"github.com/camuig/rus-trader/internal/indicators"
)

func mkSnap(ticker string, open1d, close1d, open3d, close3d, open1w, close1w float64, ema9, ema21 float64) broker.CandleSnapshot {
	return broker.CandleSnapshot{
		Ticker:   ticker,
		Period1d: broker.PeriodOHLCV{Open: open1d, Close: close1d},
		Period3d: broker.PeriodOHLCV{Open: open3d, Close: close3d},
		Period1w: broker.PeriodOHLCV{Open: open1w, Close: close1w},
		Indicators: indicators.Indicators{
			EMA9:  ema9,
			EMA21: ema21,
		},
	}
}

func TestComputeMarketContext_Uptrend(t *testing.T) {
	snaps := []broker.CandleSnapshot{
		mkSnap("A", 100, 101, 100, 102, 100, 104, 101, 100),
		mkSnap("B", 50, 50.5, 50, 51, 50, 52, 51, 50),
		mkSnap("C", 200, 202, 200, 203, 200, 206, 202, 200),
	}
	ctx := computeMarketContext(snaps)
	if ctx.Regime != "uptrend" {
		t.Errorf("expected uptrend regime, got %q", ctx.Regime)
	}
	if ctx.ChangePct1d <= 0 {
		t.Errorf("expected positive 1d change, got %v", ctx.ChangePct1d)
	}
}

func TestComputeMarketContext_Downtrend(t *testing.T) {
	snaps := []broker.CandleSnapshot{
		mkSnap("A", 100, 99, 100, 98, 100, 96, 99, 100),
		mkSnap("B", 50, 49.5, 50, 49, 50, 48, 49, 50),
	}
	ctx := computeMarketContext(snaps)
	if ctx.Regime != "downtrend" {
		t.Errorf("expected downtrend regime, got %q", ctx.Regime)
	}
}

func TestComputeMarketContext_Range(t *testing.T) {
	snaps := []broker.CandleSnapshot{
		mkSnap("A", 100, 100.2, 100, 100.1, 100, 100.3, 100, 100),
		mkSnap("B", 50, 49.9, 50, 50.05, 50, 50.1, 50, 50),
	}
	ctx := computeMarketContext(snaps)
	if ctx.Regime != "range" {
		t.Errorf("expected range regime, got %q", ctx.Regime)
	}
}

func TestComputeMarketContext_Empty(t *testing.T) {
	ctx := computeMarketContext(nil)
	if ctx.Regime != "unknown" {
		t.Errorf("expected unknown regime for empty input, got %q", ctx.Regime)
	}
}
