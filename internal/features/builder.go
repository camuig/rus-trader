package features

import (
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/camuig/rus-trader/internal/broker"
	"github.com/camuig/rus-trader/internal/dividends"
)

// TickerFeature содержит тикер и сформированную текстовую строку признаков.
type TickerFeature struct {
	Ticker  string
	Summary string
}

// BuildTickerFeatures конвертирует числовой снэпшот и опциональные данные о дивидендах
// в компактную текстовую строку для AI-анализа.
func BuildTickerFeatures(snap broker.CandleSnapshot, div *dividends.DividendInfo) TickerFeature {
	ind := snap.Indicators
	price := snap.LastPrice
	if price == 0 {
		price = snap.Period1d.Close
	}

	var parts []string

	// 1. Тренд по EMA9 vs EMA21
	if ind.EMA21 > 0 {
		gap := (ind.EMA9 - ind.EMA21) / ind.EMA21 * 100
		switch {
		case gap > 0.3:
			parts = append(parts, fmt.Sprintf("аптренд (EMA +%.1f%%)", gap))
		case gap < -0.3:
			parts = append(parts, fmt.Sprintf("даунтренд (EMA %.1f%%)", gap))
		default:
			parts = append(parts, "флэт")
		}
	}

	// 2. Серия свечей (streak) по period1d и period3d
	p1d := snap.Period1d
	p3d := snap.Period3d
	if p1d.Open > 0 && p3d.Open > 0 {
		up1d := p1d.Close > p1d.Open
		up3d := p3d.Close > p3d.Open
		if up1d && up3d && p3d.Open > 0 {
			chg := (p3d.Close - p3d.Open) / p3d.Open * 100
			parts = append(parts, fmt.Sprintf("рост 3+ дней (+%.1f%%)", chg))
		} else if !up1d && !up3d {
			chg := (p3d.Close - p3d.Open) / p3d.Open * 100
			parts = append(parts, fmt.Sprintf("падение 3+ дней (%.1f%%)", chg))
		}
	}

	// 3. Объём
	rv := ind.RelVolume
	switch {
	case rv >= 2.0:
		parts = append(parts, fmt.Sprintf("объём высокий (%.1fx)", rv))
	case rv >= 1.5:
		parts = append(parts, fmt.Sprintf("объём повышен (%.1fx)", rv))
	case rv >= 1.0:
		parts = append(parts, "объём обычный")
	case rv > 0:
		parts = append(parts, fmt.Sprintf("объём низкий (%.1fx)", rv))
	default:
		parts = append(parts, "объём неизвестен")
	}

	// 4. RSI
	rsi := ind.RSI14
	switch {
	case rsi <= 0:
		parts = append(parts, "RSI нет данных")
	case rsi < 30:
		parts = append(parts, fmt.Sprintf("RSI %.0f — перепродан", rsi))
	case rsi < 40:
		parts = append(parts, fmt.Sprintf("RSI %.0f — низкий", rsi))
	case rsi <= 60:
		parts = append(parts, fmt.Sprintf("RSI %.0f — нейтральный", rsi))
	case rsi <= 70:
		parts = append(parts, fmt.Sprintf("RSI %.0f — повышен", rsi))
	case rsi <= 80:
		parts = append(parts, fmt.Sprintf("RSI %.0f — высокий", rsi))
	default:
		parts = append(parts, fmt.Sprintf("RSI %.0f — перекуплен", rsi))
	}

	// 5. Близость к уровням (Support/Resistance в единицах ATR)
	atr := ind.ATR14
	if atr > 0 {
		if ind.Resistance > 0 {
			distR := (ind.Resistance - price) / atr
			if distR <= 1.5 {
				parts = append(parts, fmt.Sprintf("у сопротивления %.2f (%.1f ATR)", ind.Resistance, distR))
			}
		}
		if ind.Support > 0 {
			distS := (price - ind.Support) / atr
			if distS <= 1.5 {
				parts = append(parts, fmt.Sprintf("у поддержки %.2f (%.1f ATR)", ind.Support, distS))
			}
		}
	}

	// 6. Паттерны
	uptrend := ind.EMA21 > 0 && (ind.EMA9-ind.EMA21)/ind.EMA21*100 > 0.3

	// Breakout: цена >= resistance*0.995 И RelVolume > 1.5 И аптренд
	if ind.Resistance > 0 && price >= ind.Resistance*0.995 && ind.RelVolume > 1.5 && uptrend {
		parts = append(parts, "пробой сопротивления на объёме")
	}

	// Bounce: цена в пределах 1 ATR от support И RSI < 40
	if ind.Support > 0 && atr > 0 && math.Abs(price-ind.Support) <= atr && rsi > 0 && rsi < 40 {
		parts = append(parts, "отскок от поддержки")
	}

	// Compression: (resistance-support)/ATR < 2.5
	if ind.Resistance > 0 && ind.Support > 0 && atr > 0 {
		rangeATR := (ind.Resistance - ind.Support) / atr
		if rangeATR < 2.5 {
			parts = append(parts, "сжатие диапазона")
		}
	}

	// Doji: abs(close-open)/(high-low) < 0.15 на period1d
	if p1d.High > p1d.Low {
		bodyRatio := math.Abs(p1d.Close-p1d.Open) / (p1d.High - p1d.Low)
		if bodyRatio < 0.15 {
			parts = append(parts, "свеча неопределённости")
		}
	}

	// 7. Волатильность ATR%
	if atr > 0 && price > 0 {
		atrPct := atr / price * 100
		parts = append(parts, fmt.Sprintf("ATR %.1f%%", atrPct))
	}

	// 8. Дивиденд
	if div != nil && !div.ExDivDate.IsZero() && div.ExDivDate.After(time.Now()) {
		days := int(math.Round(time.Until(div.ExDivDate).Hours() / 24))
		parts = append(parts, fmt.Sprintf("дивиденд %.1f%% (отсечка через %d дн)", div.YieldPct, days))
	}

	header := fmt.Sprintf("%s (%.2f): ", snap.Ticker, price)
	summary := header + strings.Join(parts, ", ") + "."

	return TickerFeature{
		Ticker:  snap.Ticker,
		Summary: summary,
	}
}
