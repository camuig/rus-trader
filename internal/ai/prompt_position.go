package ai

import (
	"fmt"
	"strings"
)

const positionSystemPrompt = `Роль: Управляющий позициями MOEX.
Задача: Для каждой открытой позиции реши — HOLD или SELL.

HOLD — дефолт. Позиция должна работать.
SELL только если:
  (а) Цена у стоп-лосса и структура сломана (тренд развернулся, поддержка пробита).
  (б) Серьёзный фундаментальный негатив (не просто "импульс ослабел").
  (в) Позиция удерживается >2 дней без прогресса к TP.
  (г) Оригинальная гипотеза входа опровергнута фактами.

НЕ закрывай прибыльные трендовые позиции.
НЕ закрывай ради "ротации" или "ребалансировки".

Для каждой позиции верни HOLD или SELL с reasoning.
Формат: JSON массив. HOLD тоже включай.
[{"action":"HOLD","ticker":"SBER","confidence":0,"reasoning":"Причина"},
 {"action":"SELL","ticker":"GAZP","confidence":0,"reasoning":"Причина"}]
`

func BuildPositionPrompt(req *PositionRequest, maxChars int) string {
	if maxChars <= 0 {
		maxChars = 8000
	}

	var sb strings.Builder

	if !req.CurrentTime.IsZero() {
		sb.WriteString(fmt.Sprintf("## Время: %s MSK\n\n", req.CurrentTime.Format("02.01.2006 15:04")))
	}

	if req.Market.IndexTicker != "" {
		sb.WriteString(fmt.Sprintf("## Рынок: %s — %s (1д %+.2f%%)\n\n",
			req.Market.IndexTicker, req.Market.Regime, req.Market.ChangePct1d))
	}

	if len(req.Lessons) > 0 {
		sb.WriteString("## Уроки\n")
		for _, l := range req.Lessons {
			sb.WriteString("- ")
			sb.WriteString(l)
			sb.WriteString("\n")
		}
		sb.WriteString("\n")
	}

	sb.WriteString("## Открытые позиции\n\n")
	for _, p := range req.Positions {
		sb.WriteString(fmt.Sprintf("### %s\n", p.Ticker))
		sb.WriteString(fmt.Sprintf("Вход: %.2f, текущая: %.2f, P&L: %+.2f%%\n", p.EntryPrice, p.CurrentPrice, p.PnLPct))
		sb.WriteString(fmt.Sprintf("Кол-во: %d, удержание: %s\n", p.Quantity, p.HoldDuration))
		sb.WriteString(fmt.Sprintf("SL: %.2f, TP: %.2f (прогресс: %.0f%%)\n", p.StopLoss, p.TakeProfit, p.ProgressToTP))
		if p.Hypothesis != "" {
			sb.WriteString(fmt.Sprintf("Гипотеза: %s\n", p.Hypothesis))
		}
		if p.Features != "" {
			sb.WriteString(fmt.Sprintf("Текущее: %s\n", p.Features))
		}
		for _, n := range p.News {
			sb.WriteString(fmt.Sprintf("  Новость: %s\n", n))
		}
		sb.WriteString("\n")
	}

	sb.WriteString("Для каждой позиции: HOLD или SELL с причиной.")

	result := sb.String()
	if len([]rune(result)) > maxChars {
		r := []rune(result)
		result = string(r[:maxChars-1]) + "…"
	}
	return result
}
