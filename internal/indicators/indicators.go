package indicators

import "math"

// Indicators holds computed technical indicators for a ticker.
type Indicators struct {
	RSI14      float64 // RSI(14), 0-100
	EMA9       float64 // EMA(9) of close prices
	EMA21      float64 // EMA(21) of close prices
	ATR14      float64 // Average True Range(14)
	RelVolume  float64 // current volume / average volume ratio
	Support    float64 // nearest support level
	Resistance float64 // nearest resistance level
}

// Candle represents a single OHLCV candle.
type Candle struct {
	Open   float64
	High   float64
	Low    float64
	Close  float64
	Volume float64
}

// Compute calculates all technical indicators from hourly candles.
// Candles must be sorted chronologically (oldest first).
func Compute(candles []Candle) Indicators {
	if len(candles) < 2 {
		return Indicators{}
	}

	closes := make([]float64, len(candles))
	for i, c := range candles {
		closes[i] = c.Close
	}

	ind := Indicators{
		RSI14:     calcRSI(closes, 14),
		EMA9:      calcEMA(closes, 9),
		EMA21:     calcEMA(closes, 21),
		ATR14:     calcATR(candles, 14),
		RelVolume: calcRelativeVolume(candles, 20),
	}
	ind.Support, ind.Resistance = calcSupportResistance(candles)
	return ind
}

// calcRSI calculates RSI with the given period using Wilder's smoothing.
func calcRSI(closes []float64, period int) float64 {
	if len(closes) < period+1 {
		return 50 // neutral if not enough data
	}

	var avgGain, avgLoss float64

	// Initial average from first `period` changes
	for i := 1; i <= period; i++ {
		change := closes[i] - closes[i-1]
		if change > 0 {
			avgGain += change
		} else {
			avgLoss += -change
		}
	}
	avgGain /= float64(period)
	avgLoss /= float64(period)

	// Wilder's smoothing for subsequent values
	for i := period + 1; i < len(closes); i++ {
		change := closes[i] - closes[i-1]
		if change > 0 {
			avgGain = (avgGain*float64(period-1) + change) / float64(period)
			avgLoss = (avgLoss * float64(period-1)) / float64(period)
		} else {
			avgGain = (avgGain * float64(period-1)) / float64(period)
			avgLoss = (avgLoss*float64(period-1) + (-change)) / float64(period)
		}
	}

	if avgLoss == 0 {
		return 100
	}
	rs := avgGain / avgLoss
	return 100 - 100/(1+rs)
}

// calcEMA calculates EMA for the given period. Returns 0 if not enough data.
func calcEMA(closes []float64, period int) float64 {
	if len(closes) < period {
		if len(closes) > 0 {
			return closes[len(closes)-1]
		}
		return 0
	}

	// SMA for the initial period
	var sum float64
	for i := 0; i < period; i++ {
		sum += closes[i]
	}
	ema := sum / float64(period)

	multiplier := 2.0 / float64(period+1)
	for i := period; i < len(closes); i++ {
		ema = (closes[i]-ema)*multiplier + ema
	}
	return ema
}

// calcATR calculates Average True Range for the given period.
func calcATR(candles []Candle, period int) float64 {
	if len(candles) < 2 {
		return 0
	}

	// Calculate true ranges
	trs := make([]float64, 0, len(candles)-1)
	for i := 1; i < len(candles); i++ {
		tr := math.Max(
			candles[i].High-candles[i].Low,
			math.Max(
				math.Abs(candles[i].High-candles[i-1].Close),
				math.Abs(candles[i].Low-candles[i-1].Close),
			),
		)
		trs = append(trs, tr)
	}

	if len(trs) < period {
		// Not enough data, use simple average of available TRs
		var sum float64
		for _, tr := range trs {
			sum += tr
		}
		return sum / float64(len(trs))
	}

	// Initial ATR: SMA of first `period` TRs
	var sum float64
	for i := 0; i < period; i++ {
		sum += trs[i]
	}
	atr := sum / float64(period)

	// Wilder's smoothing
	for i := period; i < len(trs); i++ {
		atr = (atr*float64(period-1) + trs[i]) / float64(period)
	}
	return atr
}

// calcRelativeVolume calculates current volume relative to the N-period average.
func calcRelativeVolume(candles []Candle, lookback int) float64 {
	if len(candles) < 2 {
		return 1.0
	}

	current := candles[len(candles)-1].Volume
	if current == 0 {
		return 0
	}

	end := len(candles) - 1
	start := end - lookback
	if start < 0 {
		start = 0
	}
	if start == end {
		return 1.0
	}

	var sum float64
	for i := start; i < end; i++ {
		sum += candles[i].Volume
	}
	avg := sum / float64(end-start)
	if avg == 0 {
		return 1.0
	}
	return current / avg
}

// calcSupportResistance finds nearest support and resistance using local extremes.
func calcSupportResistance(candles []Candle) (support, resistance float64) {
	if len(candles) < 5 {
		return 0, 0
	}

	lastPrice := candles[len(candles)-1].Close

	var supports, resistances []float64

	// Find local minima and maxima (using 2-candle window on each side)
	for i := 2; i < len(candles)-2; i++ {
		// Local minimum
		if candles[i].Low <= candles[i-1].Low && candles[i].Low <= candles[i-2].Low &&
			candles[i].Low <= candles[i+1].Low && candles[i].Low <= candles[i+2].Low {
			if candles[i].Low < lastPrice {
				supports = append(supports, candles[i].Low)
			}
		}
		// Local maximum
		if candles[i].High >= candles[i-1].High && candles[i].High >= candles[i-2].High &&
			candles[i].High >= candles[i+1].High && candles[i].High >= candles[i+2].High {
			if candles[i].High > lastPrice {
				resistances = append(resistances, candles[i].High)
			}
		}
	}

	// Find nearest support (highest value below price)
	for _, s := range supports {
		if s > support {
			support = s
		}
	}
	// Find nearest resistance (lowest value above price)
	if len(resistances) > 0 {
		resistance = resistances[0]
		for _, r := range resistances {
			if r < resistance {
				resistance = r
			}
		}
	}

	return support, resistance
}
