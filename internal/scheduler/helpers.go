package scheduler

import (
	"fmt"
	"time"

	"github.com/camuig/rus-trader/internal/ai"
	"github.com/camuig/rus-trader/internal/broker"
)

func safeDiv(a, b float64) float64 {
	if b == 0 {
		return 0
	}
	return a / b
}

func formatDurationSince(t time.Time) string {
	d := time.Since(t)
	hours := int(d.Hours())
	if hours >= 24 {
		return fmt.Sprintf("%dд %dч", hours/24, hours%24)
	}
	return fmt.Sprintf("%dч %dм", hours, int(d.Minutes())%60)
}

// computeMarketContext derives broad market regime from the candle snapshots we already have.
// Uses averages across snapshots + share of uptrending tickers as a proxy for IMOEX.
func computeMarketContext(snapshots []broker.CandleSnapshot) ai.MarketContext {
	ctx := ai.MarketContext{IndexTicker: "MOEX (avg)", Regime: "unknown"}
	if len(snapshots) == 0 {
		return ctx
	}

	var sum1d, sum3d, sum1w float64
	var cnt1d, cnt3d, cnt1w int
	var uptrend, total int

	for _, snap := range snapshots {
		if snap.Period1d.Open > 0 {
			sum1d += (snap.Period1d.Close - snap.Period1d.Open) / snap.Period1d.Open * 100
			cnt1d++
		}
		if snap.Period3d.Open > 0 {
			sum3d += (snap.Period3d.Close - snap.Period3d.Open) / snap.Period3d.Open * 100
			cnt3d++
		}
		if snap.Period1w.Open > 0 {
			sum1w += (snap.Period1w.Close - snap.Period1w.Open) / snap.Period1w.Open * 100
			cnt1w++
		}
		total++
		if snap.Indicators.EMA9 > 0 && snap.Indicators.EMA21 > 0 && snap.Indicators.EMA9 > snap.Indicators.EMA21 {
			uptrend++
		}
	}

	if cnt1d > 0 {
		ctx.ChangePct1d = sum1d / float64(cnt1d)
	}
	if cnt3d > 0 {
		ctx.ChangePct3d = sum3d / float64(cnt3d)
	}
	if cnt1w > 0 {
		ctx.ChangePct1w = sum1w / float64(cnt1w)
	}

	if total == 0 {
		return ctx
	}
	uptrendShare := float64(uptrend) / float64(total)
	switch {
	case uptrendShare >= 0.6 && ctx.ChangePct3d > 0.5:
		ctx.Regime = "uptrend"
	case uptrendShare <= 0.3 && ctx.ChangePct3d < -0.5:
		ctx.Regime = "downtrend"
	case ctx.ChangePct1w > -1.5 && ctx.ChangePct1w < 1.5:
		ctx.Regime = "range"
	default:
		ctx.Regime = "mixed"
	}
	return ctx
}

func (s *Scheduler) isWithinTradingHours() bool {
	now := time.Now().In(s.loc)

	// Skip weekends
	weekday := now.Weekday()
	if weekday == time.Saturday || weekday == time.Sunday {
		return false
	}

	hour := now.Hour()
	minute := now.Minute()
	totalMinutes := hour*60 + minute

	// MOEX main session: 10:00 - 18:50 MSK
	return totalMinutes >= 600 && totalMinutes <= 1130
}
