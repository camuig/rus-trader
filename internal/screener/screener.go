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
}

// Screen ranks candle snapshots by technical signal strength and returns up to maxTickers.
// Tickers with score below minScore are excluded (position tickers are always included).
func Screen(snapshots []broker.CandleSnapshot, positionTickers map[string]bool, maxTickers int, minScore float64) []broker.CandleSnapshot {
	if maxTickers <= 0 {
		maxTickers = 20
	}

	var scores []Score
	for _, snap := range snapshots {
		if positionTickers[snap.Ticker] {
			continue
		}
		scores = append(scores, Score{
			Ticker: snap.Ticker,
			Points: scoreSnapshotFull(snap),
		})
	}

	// Sort by score descending — best candidates first
	sort.Slice(scores, func(i, j int) bool {
		return scores[i].Points > scores[j].Points
	})

	// Build result: position tickers first, then ranked by score
	selected := make(map[string]bool)
	var result []broker.CandleSnapshot

	// Always include position tickers
	for _, snap := range snapshots {
		if positionTickers[snap.Ticker] {
			result = append(result, snap)
			selected[snap.Ticker] = true
		}
	}

	// Add tickers by score until maxTickers reached (filter by minScore)
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
		if minScore > 0 && s.Points < minScore {
			continue
		}
		if snap, ok := snapMap[s.Ticker]; ok {
			result = append(result, snap)
			selected[s.Ticker] = true
		}
	}

	return result
}

// Scores returns screening scores for all snapshots (for diagnostics logging).
func Scores(snapshots []broker.CandleSnapshot) []Score {
	scores := make([]Score, 0, len(snapshots))
	for _, snap := range snapshots {
		scores = append(scores, Score{
			Ticker: snap.Ticker,
			Points: scoreSnapshotFull(snap),
		})
	}
	sort.Slice(scores, func(i, j int) bool {
		return scores[i].Points > scores[j].Points
	})
	return scores
}

// scoreSnapshotFull scores a snapshot using indicators plus price/level context.
func scoreSnapshotFull(snap broker.CandleSnapshot) float64 {
	ind := snap.Indicators
	points := scoreSnapshot(ind)

	price := snap.LastPrice
	if price <= 0 {
		return points
	}

	uptrend := ind.EMA9 > 0 && ind.EMA21 > 0 && ind.EMA9 > ind.EMA21

	// Rebound from support: price near support + RSI approaching oversold.
	if ind.Support > 0 && ind.ATR14 > 0 {
		distATR := (price - ind.Support) / ind.ATR14
		if distATR >= 0 && distATR <= 1.0 && ind.RSI14 < 40 {
			points += 2 // bounce setup
		}
	}

	// Breakout above resistance with volume confirmation.
	if ind.Resistance > 0 && price >= ind.Resistance*0.995 && ind.RelVolume > 1.5 && uptrend {
		points += 3 // breakout setup
	}

	return points
}

// scoreSnapshot assigns a screening score based on technical indicators.
// Score is used for ranking only — zero-scored tickers are still included.
func scoreSnapshot(ind indicators.Indicators) float64 {
	var points float64

	uptrend := ind.EMA9 > 0 && ind.EMA21 > 0 && ind.EMA9 > ind.EMA21

	// RSI extremes — oversold in uptrend is a strong bounce signal;
	// oversold in downtrend gets a tiny bonus (possible reversal) but much less weight.
	switch {
	case ind.RSI14 < 30 && uptrend:
		points += 3
	case ind.RSI14 < 30:
		points += 1
	case ind.RSI14 < 40 && uptrend:
		points += 1
	case ind.RSI14 > 70:
		points += 1
	}

	// EMA crossover potential — uptrend is a strong signal
	if uptrend {
		crossDist := (ind.EMA9 - ind.EMA21) / ind.EMA21 * 100
		if crossDist < 1 {
			points += 2 // fresh crossover
		} else {
			points += 1
		}
	}

	// Volume anomaly
	if ind.RelVolume > 2.0 {
		points += 3
	} else if ind.RelVolume > 1.5 {
		points += 2
	} else if ind.RelVolume > 1.2 {
		points += 1
	}

	// Tight range — potential breakout
	if ind.Support > 0 && ind.Resistance > 0 {
		range_ := ind.Resistance - ind.Support
		if range_ > 0 && ind.ATR14 > 0 && range_/ind.ATR14 < 3 {
			points += 1
		}
	}

	return points
}
