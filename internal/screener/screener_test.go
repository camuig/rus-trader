package screener

import (
	"testing"

	"github.com/camuig/rus-trader/internal/broker"
	"github.com/camuig/rus-trader/internal/indicators"
)

func TestScoreSnapshotFull_Uptrend(t *testing.T) {
	snap := broker.CandleSnapshot{
		Ticker:    "AAA",
		LastPrice: 100,
		Indicators: indicators.Indicators{
			RSI14:      55,
			EMA9:       101,
			EMA21:      100,
			ATR14:      1.5,
			RelVolume:  1.8,
			Support:    96,
			Resistance: 105,
		},
	}
	pts := scoreSnapshotFull(snap)
	if pts < 3 {
		t.Errorf("uptrend with elevated volume should score >=3, got %v", pts)
	}
}

func TestScoreSnapshotFull_ReboundFromSupport(t *testing.T) {
	snap := broker.CandleSnapshot{
		Ticker:    "BBB",
		LastPrice: 100.5, // 0.5 above support
		Indicators: indicators.Indicators{
			RSI14:      32,
			EMA9:       100,
			EMA21:      101,
			ATR14:      1.0,
			RelVolume:  1.0,
			Support:    100,
			Resistance: 108,
		},
	}
	pts := scoreSnapshotFull(snap)
	// Expected: bounce bonus(2); RSI 32 is not <30, not uptrend.
	if pts < 2 {
		t.Errorf("rebound setup should score >=2, got %v", pts)
	}
}

func TestScoreSnapshotFull_Breakout(t *testing.T) {
	snap := broker.CandleSnapshot{
		Ticker:    "CCC",
		LastPrice: 110, // breakout above resistance 108
		Indicators: indicators.Indicators{
			RSI14:      60,
			EMA9:       109,
			EMA21:      105,
			ATR14:      2.0,
			RelVolume:  2.5,
			Support:    100,
			Resistance: 108,
		},
	}
	pts := scoreSnapshotFull(snap)
	// EMA uptrend(1) + RelVol>2 (3) + breakout(3) = 7+
	if pts < 6 {
		t.Errorf("breakout setup should score >=6, got %v", pts)
	}
}

func TestScoreSnapshotFull_Downtrend(t *testing.T) {
	snap := broker.CandleSnapshot{
		Ticker:    "DDD",
		LastPrice: 50,
		Indicators: indicators.Indicators{
			RSI14:      50,
			EMA9:       49,
			EMA21:      52,
			ATR14:      1.0,
			RelVolume:  0.8,
			Support:    40,
			Resistance: 60,
		},
	}
	pts := scoreSnapshotFull(snap)
	if pts > 0 {
		t.Errorf("plain downtrend without signals should score 0, got %v", pts)
	}
}

func TestScreen_RespectsMinScore(t *testing.T) {
	snaps := []broker.CandleSnapshot{
		{
			Ticker: "GOOD", LastPrice: 100,
			Indicators: indicators.Indicators{
				RSI14: 55, EMA9: 101, EMA21: 100, ATR14: 1.5, RelVolume: 2.2,
				Support: 95, Resistance: 110,
			},
		},
		{
			Ticker: "BAD", LastPrice: 50,
			Indicators: indicators.Indicators{
				RSI14: 50, EMA9: 49, EMA21: 52, ATR14: 0.5, RelVolume: 0.5,
			},
		},
	}
	got := Screen(snaps, map[string]bool{}, 10, 2.0)
	if len(got) != 1 || got[0].Ticker != "GOOD" {
		t.Errorf("Screen should keep only GOOD with minScore=2, got %+v", got)
	}
}

func TestScreen_PositionTickersAlwaysIncluded(t *testing.T) {
	snaps := []broker.CandleSnapshot{
		{
			Ticker: "OPEN", LastPrice: 50,
			Indicators: indicators.Indicators{
				RSI14: 50, EMA9: 49, EMA21: 52, ATR14: 0.5, RelVolume: 0.5,
			},
		},
		{
			Ticker: "OTHER", LastPrice: 100,
			Indicators: indicators.Indicators{
				RSI14: 55, EMA9: 101, EMA21: 100, ATR14: 1.5, RelVolume: 2.2,
			},
		},
	}
	got := Screen(snaps, map[string]bool{"OPEN": true}, 10, 10)
	var hasOpen bool
	for _, s := range got {
		if s.Ticker == "OPEN" {
			hasOpen = true
		}
	}
	if !hasOpen {
		t.Errorf("position ticker must be included regardless of minScore; got %+v", got)
	}
}
