package screener

import (
	"sort"

	"github.com/camuig/rus-trader/internal/broker"
	"github.com/camuig/rus-trader/internal/indicators"
)

// Score represents a ticker's screening score.
type Score struct {
	Ticker string
	Points float64
	Reason string
}

// Screen filters and ranks candle snapshots by technical signal strength.
// Returns up to maxTickers best candidates. Position tickers are always included.
func Screen(snapshots []broker.CandleSnapshot, positionTickers map[string]bool, maxTickers int) []broker.CandleSnapshot {
	if maxTickers <= 0 {
		maxTickers = 5
	}

	var scores []Score
	for _, snap := range snapshots {
		// Always include tickers with open positions
		if positionTickers[snap.Ticker] {
			continue
		}
		points := scoreSnapshot(snap.Indicators)
		if points > 0 {
			scores = append(scores, Score{
				Ticker: snap.Ticker,
				Points: points,
			})
		}
	}

	// Sort by score descending
	sort.Slice(scores, func(i, j int) bool {
		return scores[i].Points > scores[j].Points
	})

	// Build result: position tickers first, then top screened
	selected := make(map[string]bool)
	var result []broker.CandleSnapshot

	// Always include position tickers
	for _, snap := range snapshots {
		if positionTickers[snap.Ticker] {
			result = append(result, snap)
			selected[snap.Ticker] = true
		}
	}

	// Add top-scored tickers up to maxTickers
	snapMap := make(map[string]broker.CandleSnapshot, len(snapshots))
	for _, snap := range snapshots {
		snapMap[snap.Ticker] = snap
	}

	for _, s := range scores {
		if len(result) >= maxTickers {
			break
		}
		if selected[s.Ticker] {
			continue
		}
		if snap, ok := snapMap[s.Ticker]; ok {
			result = append(result, snap)
			selected[s.Ticker] = true
		}
	}

	return result
}

// scoreSnapshot assigns a screening score based on technical indicators.
func scoreSnapshot(ind indicators.Indicators) float64 {
	var points float64

	// RSI extremes (potential reversal)
	if ind.RSI14 < 30 {
		points += 3 // oversold — strong buy signal
	} else if ind.RSI14 < 40 {
		points += 1 // approaching oversold
	} else if ind.RSI14 > 70 {
		points += 1 // overbought — potential sell signal (still interesting)
	}

	// EMA crossover potential
	if ind.EMA9 > ind.EMA21 && ind.EMA9 > 0 && ind.EMA21 > 0 {
		// Bullish: EMA9 above EMA21
		crossDist := (ind.EMA9 - ind.EMA21) / ind.EMA21 * 100
		if crossDist < 1 {
			points += 2 // recent crossover, strong signal
		} else {
			points += 1 // established uptrend
		}
	}

	// Volume anomaly
	if ind.RelVolume > 2.0 {
		points += 3 // very high volume — major signal
	} else if ind.RelVolume > 1.5 {
		points += 2 // elevated volume
	}

	// Support/Resistance proximity
	if ind.Support > 0 && ind.Resistance > 0 {
		range_ := ind.Resistance - ind.Support
		if range_ > 0 {
			// Close to support = potential bounce
			// We don't have lastPrice here, but ATR can indicate compressed range
			if ind.ATR14 > 0 && range_/ind.ATR14 < 3 {
				points += 1 // tight range, potential breakout
			}
		}
	}

	return points
}
