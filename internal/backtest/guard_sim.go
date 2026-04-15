package backtest

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/camuig/rus-trader/internal/config"
)

type ParsedFeatures struct {
	IsUptrend     bool
	ATRPct        float64
	RSI           float64
	HasIndicators bool
}

type SimState struct {
	OpenPositions int
	DailyPnL      float64
	DailyTrades   int
	IsUptrend     bool
	ATRPct        float64
	RSI           float64
	HasIndicators bool
}

type GuardSimulator struct {
	config config.TradingConfig
}

var (
	atrRegex = regexp.MustCompile(`ATR (\d+\.?\d*)%`)
	rsiRegex = regexp.MustCompile(`RSI (\d+\.?\d*)`)
)

func NewGuardSimulator(cfg config.TradingConfig) *GuardSimulator {
	return &GuardSimulator{config: cfg}
}

func ParseFeatures(text string) ParsedFeatures {
	var f ParsedFeatures
	if text == "" {
		return f
	}

	lower := strings.ToLower(text)
	f.IsUptrend = strings.Contains(lower, "аптренд")
	if strings.Contains(lower, "аптренд") || strings.Contains(lower, "даунтренд") || strings.Contains(lower, "флэт") {
		f.HasIndicators = true
	}

	if m := atrRegex.FindStringSubmatch(text); len(m) > 1 {
		if v, err := strconv.ParseFloat(m[1], 64); err == nil {
			f.ATRPct = v
			f.HasIndicators = true
		}
	}

	if m := rsiRegex.FindStringSubmatch(text); len(m) > 1 {
		if v, err := strconv.ParseFloat(m[1], 64); err == nil {
			f.RSI = v
			f.HasIndicators = true
		}
	}

	return f
}

// WouldAllow checks if a BUY trade with given confidence, SL/TP pcts would pass guard.
func (gs *GuardSimulator) WouldAllow(confidence float64, slPct float64, tpPct float64, state SimState) (bool, string) {
	cfg := gs.config

	if int(confidence) < cfg.MinConfidence {
		return false, fmt.Sprintf("confidence %.0f < %d", confidence, cfg.MinConfidence)
	}

	if cfg.RequireUptrend && state.HasIndicators && !state.IsUptrend {
		return false, "downtrend (RequireUptrend=true)"
	}

	if state.HasIndicators && state.RSI > 80 {
		return false, fmt.Sprintf("RSI %.0f > 80", state.RSI)
	}

	if cfg.MinATRPct > 0 && state.HasIndicators && state.ATRPct > 0 && state.ATRPct < cfg.MinATRPct {
		return false, fmt.Sprintf("ATR %.1f%% < %.1f%%", state.ATRPct, cfg.MinATRPct)
	}

	if cfg.MaxDailyLossRub > 0 && state.DailyPnL < -cfg.MaxDailyLossRub {
		return false, fmt.Sprintf("daily loss %.0f > limit %.0f", -state.DailyPnL, cfg.MaxDailyLossRub)
	}

	if slPct > 0 && tpPct > 0 && cfg.MinRiskRewardRatio > 0 {
		rr := tpPct / slPct
		if rr < cfg.MinRiskRewardRatio {
			return false, fmt.Sprintf("R:R %.1f < %.1f", rr, cfg.MinRiskRewardRatio)
		}
	}

	return true, ""
}

// SimStateFromTrade builds SimState from a trade's EntryFeatures.
func SimStateFromTrade(entryFeatures string, dailyPnL float64, openPositions int) SimState {
	pf := ParseFeatures(entryFeatures)
	return SimState{
		OpenPositions: openPositions,
		DailyPnL:      dailyPnL,
		IsUptrend:     pf.IsUptrend,
		ATRPct:        pf.ATRPct,
		RSI:           pf.RSI,
		HasIndicators: pf.HasIndicators,
	}
}
