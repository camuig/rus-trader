package storage

// DashboardMetrics — агрегированные торговые метрики для дашборда.
type DashboardMetrics struct {
	ProfitFactor       float64
	MaxDrawdownRub     float64
	WinRate            float64
	AvgWinRub          float64
	AvgLossRub         float64
	AvgStopSlippagePct float64
	ClosedTrades       int
}

// maxSlippagePct — порог отсечения битых данных: проезд стопа больше этого
// значения считается аномалией (например, из-за санитарных проблем с ценой)
// и не учитывается в среднем.
const maxSlippagePct = 50.0

// ComputeDashboardMetrics считает торговые метрики по сделкам в хронологическом
// порядке (BUY и SELL). Прибыль, просадка и win rate считаются по закрытым SELL
// с ненулевым PnL; проезд стопа — по парам "BUY того же тикера → SELL ниже его SL".
func ComputeDashboardMetrics(trades []Trade) DashboardMetrics {
	var m DashboardMetrics

	var totalProfit, totalLoss float64 // totalLoss хранится как положительное число
	var wins, losses int
	var cumulative, peak, maxDrawdown float64
	var slippageSum float64
	var slippageCount int

	lastBuy := make(map[string]Trade)

	for _, t := range trades {
		if t.Action == "BUY" {
			lastBuy[t.Ticker] = t
			continue
		}
		if t.Action != "SELL" {
			continue
		}

		if buy, ok := lastBuy[t.Ticker]; ok && buy.StopLossPrice > 0 && t.Price < buy.StopLossPrice {
			slippage := (buy.StopLossPrice - t.Price) / buy.StopLossPrice * 100
			if slippage <= maxSlippagePct {
				slippageSum += slippage
				slippageCount++
			}
		}

		if t.PnL == 0 {
			continue
		}
		m.ClosedTrades++

		cumulative += t.PnL
		if cumulative > peak {
			peak = cumulative
		}
		if drawdown := peak - cumulative; drawdown > maxDrawdown {
			maxDrawdown = drawdown
		}

		if t.PnL > 0 {
			wins++
			totalProfit += t.PnL
		} else {
			losses++
			totalLoss += -t.PnL
		}
	}

	if totalLoss > 0 {
		m.ProfitFactor = totalProfit / totalLoss
	}
	if m.ClosedTrades > 0 {
		m.WinRate = float64(wins) / float64(m.ClosedTrades) * 100
	}
	if wins > 0 {
		m.AvgWinRub = totalProfit / float64(wins)
	}
	if losses > 0 {
		m.AvgLossRub = -totalLoss / float64(losses)
	}
	if slippageCount > 0 {
		m.AvgStopSlippagePct = slippageSum / float64(slippageCount)
	}
	m.MaxDrawdownRub = maxDrawdown

	return m
}
