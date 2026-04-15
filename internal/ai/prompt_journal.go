package ai

import (
	"fmt"
	"strings"
)

const journalSystemPrompt = `Роль: Аналитик торговых результатов.
Задача: Проанализируй закрытые сделки и сформулируй 3-5 конкретных уроков.

Для каждого урока:
- pattern: какой паттерн/сетап/ситуация
- observation: что показали данные
- recommendation: что делать иначе
- confidence: "low" (1-2 сделки), "medium" (3-4), "high" (5+)

Примеры хороших уроков:
- "Пробой resistance на низком объёме (RelVol < 1.3) в 4 из 5 случаев оказался ложным → повысить порог объёма"
- "Позиции открытые в первый час торгов имеют win rate 35% → предпочитать входы после 12:00"
- "SELL по AI с reasoning 'ослабление импульса' в 6 из 8 случаев был преждевременным → ждать пробоя SL"

Формат: ТОЛЬКО JSON массив.
[{"pattern":"...","observation":"...","recommendation":"...","confidence":"medium","tickers":["SBER"]}]
`

func BuildJournalReviewPrompt(req *JournalReviewRequest, maxChars int) string {
	if maxChars <= 0 {
		maxChars = 12000
	}

	var sb strings.Builder

	sb.WriteString(fmt.Sprintf("## Обзор сделок за %s\n\n", req.CurrentDate.Format("02.01.2006")))

	sb.WriteString("## Агрегат\n")
	sb.WriteString(fmt.Sprintf("Сделок: %d, Win rate: %.0f%%\n", req.Stats.TotalTrades, req.Stats.WinRate))
	if req.Stats.AvgWinPnL > 0 {
		sb.WriteString(fmt.Sprintf("Ср. прибыль: +%.0f ₽ (удержание %.1f ч)\n", req.Stats.AvgWinPnL, req.Stats.AvgHoldHoursWin))
	}
	if req.Stats.AvgLossPnL < 0 {
		sb.WriteString(fmt.Sprintf("Ср. убыток: %.0f ₽ (удержание %.1f ч)\n", req.Stats.AvgLossPnL, req.Stats.AvgHoldHoursLoss))
	}
	if len(req.Stats.ByExitReason) > 0 {
		sb.WriteString("По причинам: ")
		var reasons []string
		for reason, count := range req.Stats.ByExitReason {
			reasons = append(reasons, fmt.Sprintf("%s=%d", reason, count))
		}
		sb.WriteString(strings.Join(reasons, ", "))
		sb.WriteString("\n")
	}
	sb.WriteString("\n")

	sb.WriteString("## Сделки\n\n")
	for _, o := range req.Outcomes {
		sb.WriteString(fmt.Sprintf("### %s (%s, %+.0f ₽)\n", o.Ticker, o.Outcome, o.PnL))
		if o.Hypothesis != "" {
			sb.WriteString(fmt.Sprintf("Гипотеза: %s\n", o.Hypothesis))
		}
		if o.EntryFeatures != "" {
			sb.WriteString(fmt.Sprintf("При входе: %s\n", o.EntryFeatures))
		}
		sb.WriteString(fmt.Sprintf("Выход: %s. %s\n", o.ExitReason, o.WhatHappened))
		sb.WriteString(fmt.Sprintf("Удержание: %.1f ч\n\n", o.HoldHours))
	}

	if len(req.PreviousLessons) > 0 {
		sb.WriteString("## Предыдущие уроки\n")
		for _, l := range req.PreviousLessons {
			sb.WriteString("- ")
			sb.WriteString(l)
			sb.WriteString("\n")
		}
		sb.WriteString("\n")
	}

	sb.WriteString("Сформулируй 3-5 уроков в JSON.")

	result := sb.String()
	if len([]rune(result)) > maxChars {
		r := []rune(result)
		result = string(r[:maxChars-1]) + "…"
	}
	return result
}
