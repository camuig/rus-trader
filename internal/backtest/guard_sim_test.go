package backtest

import (
	"testing"

	"github.com/camuig/rus-trader/internal/config"
)

func TestParseFeatures_Uptrend(t *testing.T) {
	f := ParseFeatures("SBER (305.20): аптренд (EMA +0.8%), объём повышен (1.8x), RSI 58 — нейтральный, ATR 3.2%.")
	if !f.IsUptrend {
		t.Error("expected uptrend")
	}
	if f.ATRPct < 3.1 || f.ATRPct > 3.3 {
		t.Errorf("expected ATR ~3.2, got %.1f", f.ATRPct)
	}
	if f.RSI < 57 || f.RSI > 59 {
		t.Errorf("expected RSI ~58, got %.0f", f.RSI)
	}
	if !f.HasIndicators {
		t.Error("expected HasIndicators=true")
	}
}

func TestParseFeatures_Downtrend(t *testing.T) {
	f := ParseFeatures("GAZP (150.00): даунтренд (EMA -1.2%), RSI 35 — низкий, ATR 2.1%.")
	if f.IsUptrend {
		t.Error("expected downtrend")
	}
	if f.ATRPct < 2.0 || f.ATRPct > 2.2 {
		t.Errorf("expected ATR ~2.1, got %.1f", f.ATRPct)
	}
}

func TestParseFeatures_Empty(t *testing.T) {
	f := ParseFeatures("")
	if f.IsUptrend || f.ATRPct != 0 || f.RSI != 0 || f.HasIndicators {
		t.Error("expected zeros for empty")
	}
}

func TestWouldAllow_ConfidenceFilter(t *testing.T) {
	gs := NewGuardSimulator(config.TradingConfig{MinConfidence: 70})
	ok, _ := gs.WouldAllow(60, 0, 0, SimState{})
	if ok {
		t.Error("should block confidence 60 < 70")
	}
	ok, _ = gs.WouldAllow(80, 0, 0, SimState{})
	if !ok {
		t.Error("should allow confidence 80")
	}
}

func TestWouldAllow_UptrendFilter(t *testing.T) {
	gs := NewGuardSimulator(config.TradingConfig{MinConfidence: 70, RequireUptrend: true})
	ok, _ := gs.WouldAllow(80, 0, 0, SimState{IsUptrend: false, HasIndicators: true})
	if ok {
		t.Error("should block downtrend")
	}
	ok, _ = gs.WouldAllow(80, 0, 0, SimState{IsUptrend: true, HasIndicators: true})
	if !ok {
		t.Error("should allow uptrend")
	}
}

func TestWouldAllow_RSIFilter(t *testing.T) {
	gs := NewGuardSimulator(config.TradingConfig{MinConfidence: 70})
	ok, _ := gs.WouldAllow(80, 0, 0, SimState{RSI: 85, HasIndicators: true})
	if ok {
		t.Error("should block RSI > 80")
	}
}

func TestWouldAllow_DailyLoss(t *testing.T) {
	gs := NewGuardSimulator(config.TradingConfig{MinConfidence: 70, MaxDailyLossRub: 500})
	ok, _ := gs.WouldAllow(80, 0, 0, SimState{DailyPnL: -600})
	if ok {
		t.Error("should block daily loss > limit")
	}
}

func TestWouldAllow_RiskReward(t *testing.T) {
	gs := NewGuardSimulator(config.TradingConfig{MinConfidence: 70, MinRiskRewardRatio: 1.5})
	ok, _ := gs.WouldAllow(80, 3.0, 3.0, SimState{}) // RR = 1.0
	if ok {
		t.Error("should block RR 1.0 < 1.5")
	}
	ok, _ = gs.WouldAllow(80, 2.0, 4.0, SimState{}) // RR = 2.0
	if !ok {
		t.Error("should allow RR 2.0 >= 1.5")
	}
}
