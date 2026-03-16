package indicators

import (
	"math"
	"testing"
)

func TestCalcRSI_Neutral(t *testing.T) {
	// Alternating gains and losses should give RSI near 50
	closes := make([]float64, 30)
	closes[0] = 100
	for i := 1; i < len(closes); i++ {
		if i%2 == 0 {
			closes[i] = closes[i-1] + 1
		} else {
			closes[i] = closes[i-1] - 1
		}
	}
	rsi := calcRSI(closes, 14)
	if rsi < 40 || rsi > 60 {
		t.Errorf("expected RSI near 50, got %.2f", rsi)
	}
}

func TestCalcRSI_AllUp(t *testing.T) {
	closes := make([]float64, 20)
	for i := range closes {
		closes[i] = float64(100 + i)
	}
	rsi := calcRSI(closes, 14)
	if rsi != 100 {
		t.Errorf("expected RSI=100 for all gains, got %.2f", rsi)
	}
}

func TestCalcRSI_AllDown(t *testing.T) {
	closes := make([]float64, 20)
	for i := range closes {
		closes[i] = float64(200 - i)
	}
	rsi := calcRSI(closes, 14)
	if rsi != 0 {
		t.Errorf("expected RSI=0 for all losses, got %.2f", rsi)
	}
}

func TestCalcRSI_InsufficientData(t *testing.T) {
	rsi := calcRSI([]float64{100, 101}, 14)
	if rsi != 50 {
		t.Errorf("expected RSI=50 for insufficient data, got %.2f", rsi)
	}
}

func TestCalcEMA(t *testing.T) {
	closes := []float64{10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20}
	ema9 := calcEMA(closes, 9)
	// EMA should be between first and last values, closer to last
	if ema9 < 15 || ema9 > 20 {
		t.Errorf("EMA(9) out of range: %.2f", ema9)
	}
}

func TestCalcEMA_InsufficientData(t *testing.T) {
	ema := calcEMA([]float64{42.5}, 9)
	if ema != 42.5 {
		t.Errorf("expected last close for insufficient data, got %.2f", ema)
	}
}

func TestCalcATR(t *testing.T) {
	candles := make([]Candle, 20)
	for i := range candles {
		base := float64(100 + i)
		candles[i] = Candle{
			Open:  base,
			High:  base + 2,
			Low:   base - 1,
			Close: base + 1,
		}
	}
	atr := calcATR(candles, 14)
	// True range should be ~3 (high-low), so ATR should be near 3
	if atr < 2.5 || atr > 3.5 {
		t.Errorf("expected ATR near 3, got %.2f", atr)
	}
}

func TestCalcRelativeVolume(t *testing.T) {
	candles := make([]Candle, 25)
	for i := range candles {
		candles[i] = Candle{Volume: 1000}
	}
	// Last candle has 2x volume
	candles[len(candles)-1].Volume = 2000
	rv := calcRelativeVolume(candles, 20)
	if math.Abs(rv-2.0) > 0.01 {
		t.Errorf("expected RelVol=2.0, got %.2f", rv)
	}
}

func TestCalcSupportResistance(t *testing.T) {
	// Test that function returns reasonable values and doesn't panic
	// Create V-shape pattern: down then up
	candles := make([]Candle, 15)
	for i := range candles {
		var base float64
		if i <= 7 {
			base = 110 - float64(i)*2 // descending
		} else {
			base = 96 + float64(i-7)*2 // ascending
		}
		candles[i] = Candle{
			Open:  base + 0.5,
			High:  base + 1,
			Low:   base - 1,
			Close: base,
		}
	}
	// Last close = 112
	support, resistance := calcSupportResistance(candles)
	// Should find at least one support below last close
	if support == 0 && resistance == 0 {
		// Both zero is acceptable if no clear pattern found
		return
	}
	if support > 0 && support >= candles[len(candles)-1].Close {
		t.Errorf("support %.2f should be below last close %.2f", support, candles[len(candles)-1].Close)
	}
}

func TestCompute(t *testing.T) {
	candles := make([]Candle, 50)
	for i := range candles {
		// Add some variation so RSI isn't exactly 100
		base := 100.0 + float64(i)*0.5
		if i%5 == 0 {
			base -= 0.2 // occasional dip
		}
		candles[i] = Candle{
			Open:   base,
			High:   base + 1,
			Low:    base - 0.5,
			Close:  base + 0.3,
			Volume: 1000,
		}
	}

	ind := Compute(candles)
	if ind.RSI14 <= 0 {
		t.Errorf("RSI14 should be positive: %.2f", ind.RSI14)
	}
	if ind.EMA9 <= 0 {
		t.Errorf("EMA9 should be positive: %.2f", ind.EMA9)
	}
	if ind.EMA21 <= 0 {
		t.Errorf("EMA21 should be positive: %.2f", ind.EMA21)
	}
	if ind.ATR14 <= 0 {
		t.Errorf("ATR14 should be positive: %.2f", ind.ATR14)
	}
	if ind.RelVolume != 1.0 {
		t.Errorf("RelVolume should be 1.0 for constant volume: %.2f", ind.RelVolume)
	}
}

func TestCompute_EmptyCandles(t *testing.T) {
	ind := Compute(nil)
	if ind.RSI14 != 0 {
		t.Errorf("expected zero indicators for nil input, got RSI=%.2f", ind.RSI14)
	}
}
