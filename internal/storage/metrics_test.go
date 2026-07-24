package storage

import "testing"

func almostEqual(a, b float64) bool {
	const eps = 0.001
	diff := a - b
	if diff < 0 {
		diff = -diff
	}
	return diff < eps
}

func TestComputeDashboardMetrics_Empty(t *testing.T) {
	got := ComputeDashboardMetrics(nil)
	want := DashboardMetrics{}
	if got != want {
		t.Fatalf("expected zero metrics for empty input, got %+v", got)
	}
}

func TestComputeDashboardMetrics_NormalCase(t *testing.T) {
	// 3 выигрыша (500+200+400=1100), 2 убытка (300+100=400), SL не заданы,
	// чтобы не пересекаться с расчётом проезда стопа.
	trades := []Trade{
		{Ticker: "SBER", Action: "BUY", Price: 100},
		{Ticker: "SBER", Action: "SELL", Price: 110, PnL: 500},
		{Ticker: "SBER", Action: "BUY", Price: 110},
		{Ticker: "SBER", Action: "SELL", Price: 100, PnL: -300},
		{Ticker: "SBER", Action: "BUY", Price: 100},
		{Ticker: "SBER", Action: "SELL", Price: 120, PnL: 200},
		{Ticker: "SBER", Action: "BUY", Price: 120},
		{Ticker: "SBER", Action: "SELL", Price: 110, PnL: -100},
		{Ticker: "SBER", Action: "BUY", Price: 110},
		{Ticker: "SBER", Action: "SELL", Price: 150, PnL: 400},
	}

	got := ComputeDashboardMetrics(trades)

	if got.ClosedTrades != 5 {
		t.Fatalf("ClosedTrades = %d, want 5", got.ClosedTrades)
	}
	if !almostEqual(got.ProfitFactor, 2.75) {
		t.Fatalf("ProfitFactor = %v, want 2.75", got.ProfitFactor)
	}
	if !almostEqual(got.WinRate, 60.0) {
		t.Fatalf("WinRate = %v, want 60", got.WinRate)
	}
	if !almostEqual(got.AvgWinRub, 1100.0/3.0) {
		t.Fatalf("AvgWinRub = %v, want %v", got.AvgWinRub, 1100.0/3.0)
	}
	if !almostEqual(got.AvgLossRub, -200.0) {
		t.Fatalf("AvgLossRub = %v, want -200", got.AvgLossRub)
	}
	// Кумулятивная кривая: 500, 200, 400, 300, 700; пик 500 -> просадка 300 (500-200).
	if !almostEqual(got.MaxDrawdownRub, 300.0) {
		t.Fatalf("MaxDrawdownRub = %v, want 300", got.MaxDrawdownRub)
	}
}

func TestComputeDashboardMetrics_StopSlippage(t *testing.T) {
	trades := []Trade{
		// Проезд стопа: SL=100, продажа по 90 -> 10%.
		{Ticker: "GAZP", Action: "BUY", Price: 105, StopLossPrice: 100},
		{Ticker: "GAZP", Action: "SELL", Price: 90, PnL: -150},
		// Проезд стопа: SL=50, продажа по 45 -> 10%.
		{Ticker: "LKOH", Action: "BUY", Price: 55, StopLossPrice: 50},
		{Ticker: "LKOH", Action: "SELL", Price: 45, PnL: -50},
		// Стоп не проехал (цена продажи выше SL) — не учитывается.
		{Ticker: "SBER", Action: "BUY", Price: 100, StopLossPrice: 90},
		{Ticker: "SBER", Action: "SELL", Price: 95, PnL: -50},
	}

	got := ComputeDashboardMetrics(trades)

	if !almostEqual(got.AvgStopSlippagePct, 10.0) {
		t.Fatalf("AvgStopSlippagePct = %v, want 10", got.AvgStopSlippagePct)
	}
}

func TestComputeDashboardMetrics_DiscardsExtremeSlippage(t *testing.T) {
	trades := []Trade{
		// Аномальный проезд 60% (SL=100, цена 40) — считается битыми данными и отбрасывается.
		{Ticker: "GAZP", Action: "BUY", Price: 105, StopLossPrice: 100},
		{Ticker: "GAZP", Action: "SELL", Price: 40, PnL: -650},
		// Нормальный проезд 10% (SL=50, цена 45) — учитывается.
		{Ticker: "LKOH", Action: "BUY", Price: 55, StopLossPrice: 50},
		{Ticker: "LKOH", Action: "SELL", Price: 45, PnL: -50},
	}

	got := ComputeDashboardMetrics(trades)

	if !almostEqual(got.AvgStopSlippagePct, 10.0) {
		t.Fatalf("AvgStopSlippagePct = %v, want 10 (extreme slippage should be excluded)", got.AvgStopSlippagePct)
	}
}
