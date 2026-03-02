package ai

import (
	"fmt"
	"strings"
	"time"
)

const systemPrompt = `Роль: Трейдер MOEX (горизонт 1-2 дня).
Задача: Анализ OHLCV, объемов и новостей для принятия решений (BUY, SELL, HOLD).

Алгоритм анализа:
1. Управление позициями: HOLD, если цена между SL и TP. SELL только при пробое SL, фундаментальном негативе или отсутствии прогресса > 2 дней. Шум ±0.5% игнорировать.
2. Открытие (BUY): Только при Confidence ≥65. Цель — прибыль >1.5% (учитывай комиссию 0.08%). Не покупать тикеры, которые уже в портфеле или торговались сегодня.
3. Технический анализ: Использовать уровни поддержки/сопротивления и всплески объемов на периодах 3ч, 1д, 3д, 1нед.
4. Риск-менеджмент: Лимит на позицию — 10% депо. Обязательны расчетные SL/TP.

Требования к ответу:
- Строго JSON массив объектов.
- reasoning: 1 краткое предложение.
- Если сделок нет — вернуть [].

Ответ строго в JSON (массив объектов):
[
  {
    "action": "BUY",
    "ticker": "SBER",
    "stop_loss": 250.0,
    "take_profit": 290.0,
    "confidence": 80,
    "reasoning": "Краткая причина (1 предложение)"
  }
]
`

func BuildUserPrompt(req *AnalysisRequest, todayTraded []string) string {
	var sb strings.Builder

	sb.WriteString("## Текущий портфель\n")
	sb.WriteString(fmt.Sprintf("Общий баланс: %.2f ₽ / Доступно: %.2f ₽\n\n", req.TotalRub, req.AvailableRub))

	if len(req.Positions) > 0 {
		sb.WriteString("### Открытые позиции\n")
		for _, p := range req.Positions {
			changeSinceEntry := 0.0
			if p.AvgPrice > 0 {
				changeSinceEntry = (p.CurrentPrice - p.AvgPrice) / p.AvgPrice * 100
			}
			sb.WriteString(fmt.Sprintf("- %s: %.0f шт, вход %.2f, текущая %.2f (%+.2f%%), P&L %.2f",
				p.Ticker, p.Quantity, p.AvgPrice, p.CurrentPrice, changeSinceEntry, p.PnL))
			if tc, ok := req.OpenContext[p.Ticker]; ok {
				sb.WriteString(fmt.Sprintf("\n  Открыта: %s (удержание %s)",
					tc.OpenedAt.Format("02.01 15:04"), formatDuration(tc.OpenedAt)))
				if tc.StopLossPrice > 0 || tc.TakeProfitPrice > 0 {
					sb.WriteString(fmt.Sprintf("\n  План: SL=%.2f, TP=%.2f", tc.StopLossPrice, tc.TakeProfitPrice))
					if tc.TakeProfitPrice > 0 && p.AvgPrice > 0 {
						progress := (p.CurrentPrice - p.AvgPrice) / (tc.TakeProfitPrice - p.AvgPrice) * 100
						sb.WriteString(fmt.Sprintf(" (прогресс к TP: %.0f%%)", progress))
					}
				}
				if tc.Reasoning != "" {
					sb.WriteString(fmt.Sprintf("\n  Гипотеза: %s", truncate(tc.Reasoning, 150)))
				}
			}
			sb.WriteString("\n")
		}
		sb.WriteString("\n")
	} else {
		sb.WriteString("Открытых позиций нет.\n\n")
	}

	if len(req.RecentTrades) > 0 {
		sb.WriteString("### Закрытые сделки за 24ч\n")
		for _, t := range req.RecentTrades {
			sb.WriteString(fmt.Sprintf("- %s: вход %.2f → выход %.2f, %d шт, P&L %+.2f, закрыта %s",
				t.Ticker, t.EntryPrice, t.ExitPrice, t.Quantity, t.PnL,
				t.ClosedAt.Format("02.01 15:04")))
			if t.Reasoning != "" {
				sb.WriteString(fmt.Sprintf("\n  Причина закрытия: %s", truncate(t.Reasoning, 150)))
			}
			sb.WriteString("\n")
		}
		sb.WriteString("\n")
	}

	if len(todayTraded) > 0 {
		sb.WriteString("### Тикеры, проторгованные сегодня (НЕ покупать повторно!)\n")
		sb.WriteString(strings.Join(todayTraded, ", "))
		sb.WriteString("\n\n")
	}

	sb.WriteString("## Рыночные данные OHLCV (TQBR)\n")
	for _, t := range req.Tickers {
		sb.WriteString(fmt.Sprintf("%s (%.2f):\n", t.Ticker, t.LastPrice))
		periods := []struct {
			name string
			data PeriodData
		}{
			{"3ч  ", t.Period3h},
			{"1д  ", t.Period1d},
			{"3д  ", t.Period3d},
			{"1нед", t.Period1w},
		}
		for _, p := range periods {
			sb.WriteString(fmt.Sprintf("  %s: O=%.2f H=%.2f L=%.2f V=%s %+.1f%%\n",
				p.name, p.data.Open, p.data.High, p.data.Low, formatVolume(p.data.Volume), p.data.ChangePct))
		}
	}
	sb.WriteString("\n")

	// News section
	hasNews := false
	for _, t := range req.Tickers {
		if len(t.News) > 0 {
			if !hasNews {
				sb.WriteString("## Новости за 24ч\n")
				hasNews = true
			}
			sb.WriteString(fmt.Sprintf("### %s\n", t.Ticker))
			for _, n := range t.News {
				sb.WriteString(fmt.Sprintf("- %s\n", n))
			}
		}
	}
	if !hasNews {
		sb.WriteString("## Новости за 24ч\nРелевантных новостей не найдено.\n")
	}

	sb.WriteString("\nПроанализируй и выдай решения в JSON.")

	return sb.String()
}

func formatVolume(v float64) string {
	switch {
	case v >= 1_000_000:
		return fmt.Sprintf("%.1fM", v/1_000_000)
	case v >= 1_000:
		return fmt.Sprintf("%.1fK", v/1_000)
	default:
		return fmt.Sprintf("%.0f", v)
	}
}

func formatDuration(since time.Time) string {
	d := time.Since(since)
	hours := int(d.Hours())
	minutes := int(d.Minutes()) % 60
	if hours > 0 {
		return fmt.Sprintf("%dч %dмин", hours, minutes)
	}
	return fmt.Sprintf("%dмин", minutes)
}

func truncate(s string, maxRunes int) string {
	r := []rune(s)
	if len(r) <= maxRunes {
		return s
	}
	return string(r[:maxRunes]) + "…"
}
