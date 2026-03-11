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
2. Открытие (BUY): Только при Confidence ≥65. Цель — прибыль >1.5% (учитывай комиссию 0.08%). Тикеры, которые уже в портфеле или торговались сегодня, покупать только если это сильно обосновано.
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

func BuildUserPrompt(req *AnalysisRequest, todayTraded []string, limits PromptLimits) string {
	limits = normalizePromptLimits(limits)

	tail := "\nПроанализируй и выдай решения в JSON."
	bodyLimit := limits.MaxChars - runeLen(tail)
	if bodyLimit < 0 {
		bodyLimit = limits.MaxChars
	}

	builder := &cappedBuilder{maxRunes: bodyLimit}

	builder.WriteString("## Текущий портфель\n")
	builder.WriteString(fmt.Sprintf("Общий баланс: %.2f ₽ / Доступно: %.2f ₽\n\n", req.TotalRub, req.AvailableRub))

	if len(req.Positions) > 0 {
		builder.WriteString("### Открытые позиции\n")
		for _, p := range req.Positions {
			changeSinceEntry := 0.0
			if p.AvgPrice > 0 {
				changeSinceEntry = (p.CurrentPrice - p.AvgPrice) / p.AvgPrice * 100
			}
			builder.WriteString(fmt.Sprintf("- %s: %.0f шт, вход %.2f, текущая %.2f (%+.2f%%), P&L %.2f",
				p.Ticker, p.Quantity, p.AvgPrice, p.CurrentPrice, changeSinceEntry, p.PnL))
			if tc, ok := req.OpenContext[p.Ticker]; ok {
				builder.WriteString(fmt.Sprintf("\n  Открыта: %s (удержание %s)",
					tc.OpenedAt.Format("02.01 15:04"), formatDuration(tc.OpenedAt)))
				if tc.StopLossPrice > 0 || tc.TakeProfitPrice > 0 {
					builder.WriteString(fmt.Sprintf("\n  План: SL=%.2f, TP=%.2f", tc.StopLossPrice, tc.TakeProfitPrice))
					if tc.TakeProfitPrice > 0 && p.AvgPrice > 0 {
						progress := (p.CurrentPrice - p.AvgPrice) / (tc.TakeProfitPrice - p.AvgPrice) * 100
						builder.WriteString(fmt.Sprintf(" (прогресс к TP: %.0f%%)", progress))
					}
				}
				if tc.Reasoning != "" {
					builder.WriteString(fmt.Sprintf("\n  Гипотеза: %s", truncate(tc.Reasoning, 120)))
				}
			}
			builder.WriteString("\n")
		}
		builder.WriteString("\n")
	} else {
		builder.WriteString("Открытых позиций нет.\n\n")
	}

	if len(req.RecentTrades) > 0 {
		builder.WriteString("### Закрытые сделки за 24ч\n")
		for i, t := range req.RecentTrades {
			if i >= 8 {
				builder.WriteString("- ...\n")
				break
			}
			builder.WriteString(fmt.Sprintf("- %s: вход %.2f → выход %.2f, %d шт, P&L %+.2f, закрыта %s",
				t.Ticker, t.EntryPrice, t.ExitPrice, t.Quantity, t.PnL,
				t.ClosedAt.Format("02.01 15:04")))
			if t.Reasoning != "" {
				builder.WriteString(fmt.Sprintf("\n  Причина закрытия: %s", truncate(t.Reasoning, 120)))
			}
			builder.WriteString("\n")
		}
		builder.WriteString("\n")
	}

	if len(todayTraded) > 0 {
		builder.WriteString("### Тикеры, проторгованные сегодня (НЕ покупать повторно!)\n")
		builder.WriteString(strings.Join(todayTraded, ", "))
		builder.WriteString("\n\n")
	}

	builder.WriteString("## OHLCV (TQBR)\n")
	builder.WriteString("Ticker|Price|Per|Open|High|Low|Vol|Chg%\n")
	for _, t := range req.Tickers {
		periods := []struct {
			name string
			data PeriodData
		}{
			{"3h", t.Period3h},
			{"1d", t.Period1d},
			{"3d", t.Period3d},
			{"1w", t.Period1w},
		}
		for i, p := range periods {
			if i == 0 {
				builder.WriteString(fmt.Sprintf("%s|%.2f|%s|%.2f|%.2f|%.2f|%s|%+.1f\n",
					t.Ticker, t.LastPrice, p.name, p.data.Open, p.data.High, p.data.Low, formatVolume(p.data.Volume), p.data.ChangePct))
			} else {
				builder.WriteString(fmt.Sprintf("||%s|%.2f|%.2f|%.2f|%s|%+.1f\n",
					p.name, p.data.Open, p.data.High, p.data.Low, formatVolume(p.data.Volume), p.data.ChangePct))
			}
		}
	}
	builder.WriteString("\n")

	builder.WriteString(buildTickerBriefSection(req.Tickers, limits.MaxTickerBriefChars, limits.MaxNewsTitleChars))
	builder.WriteString(buildTickerNewsSection(req.Tickers, limits.MaxTickerNewsItems, limits.MaxNewsTitleChars))
	builder.WriteString(buildWorldNewsSection(req.GlobalNews, limits.MaxWorldNewsItems, limits.MaxNewsTitleChars))

	prompt := builder.String() + tail
	if runeLen(prompt) > limits.MaxChars {
		return truncate(prompt, limits.MaxChars)
	}
	return prompt
}

type cappedBuilder struct {
	sb       strings.Builder
	maxRunes int
	used     int
}

func (b *cappedBuilder) WriteString(s string) {
	if s == "" {
		return
	}
	if b.maxRunes <= 0 {
		b.sb.WriteString(s)
		return
	}
	if b.used >= b.maxRunes {
		return
	}

	r := []rune(s)
	remaining := b.maxRunes - b.used
	if len(r) > remaining {
		b.sb.WriteString(string(r[:remaining]))
		b.used = b.maxRunes
		return
	}

	b.sb.WriteString(s)
	b.used += len(r)
}

func (b *cappedBuilder) String() string {
	return b.sb.String()
}

func normalizePromptLimits(l PromptLimits) PromptLimits {
	if l.MaxChars <= 0 {
		l.MaxChars = 12000
	}
	if l.MaxTickerBriefChars <= 0 {
		l.MaxTickerBriefChars = 900
	}
	if l.MaxTickerNewsItems <= 0 {
		l.MaxTickerNewsItems = 8
	}
	if l.MaxWorldNewsItems <= 0 {
		l.MaxWorldNewsItems = 5
	}
	if l.MaxNewsTitleChars <= 0 {
		l.MaxNewsTitleChars = 120
	}
	return l
}

func buildTickerBriefSection(tickers []TickerAnalysis, maxSectionChars, maxTitleChars int) string {
	if maxSectionChars <= 0 {
		return ""
	}

	var sb strings.Builder
	used := 0
	wroteHeader := false

	for _, t := range tickers {
		brief := sanitizePromptLine(t.Brief)
		if brief == "" {
			continue
		}
		line := fmt.Sprintf("- %s: %s\n", t.Ticker, truncate(brief, maxTitleChars))
		lineRunes := runeLen(line)
		if !wroteHeader {
			header := "## Кратко по тикерам\n"
			if runeLen(header)+lineRunes > maxSectionChars {
				break
			}
			sb.WriteString(header)
			used += runeLen(header)
			wroteHeader = true
		}
		if used+lineRunes > maxSectionChars {
			sb.WriteString("- ...\n\n")
			return sb.String()
		}
		sb.WriteString(line)
		used += lineRunes
	}

	if wroteHeader {
		sb.WriteString("\n")
	}
	return sb.String()
}

func buildTickerNewsSection(tickers []TickerAnalysis, maxItems, maxTitleChars int) string {
	if maxItems <= 0 {
		return ""
	}

	var items []string
	seen := make(map[string]struct{})
	for _, t := range tickers {
		for _, n := range t.News {
			line := fmt.Sprintf("%s: %s", t.Ticker, truncate(sanitizePromptLine(n), maxTitleChars))
			if line == "" {
				continue
			}
			key := strings.ToLower(line)
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			items = append(items, line)
			if len(items) >= maxItems {
				break
			}
		}
		if len(items) >= maxItems {
			break
		}
	}

	if len(items) == 0 {
		return ""
	}

	var sb strings.Builder
	sb.WriteString("## Новости по тикерам (24ч)\n")
	for _, item := range items {
		sb.WriteString("- ")
		sb.WriteString(item)
		sb.WriteString("\n")
	}
	sb.WriteString("\n")
	return sb.String()
}

func buildWorldNewsSection(news []string, maxItems, maxTitleChars int) string {
	if maxItems <= 0 || len(news) == 0 {
		return ""
	}

	var items []string
	seen := make(map[string]struct{})
	for _, n := range news {
		line := truncate(sanitizePromptLine(n), maxTitleChars)
		if line == "" {
			continue
		}
		key := strings.ToLower(line)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		items = append(items, line)
		if len(items) >= maxItems {
			break
		}
	}

	if len(items) == 0 {
		return ""
	}

	var sb strings.Builder
	sb.WriteString("## Общемировой фон (24ч)\n")
	for _, item := range items {
		sb.WriteString("- ")
		sb.WriteString(item)
		sb.WriteString("\n")
	}
	sb.WriteString("\n")
	return sb.String()
}

func sanitizePromptLine(s string) string {
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "\r", " ")
	s = strings.TrimSpace(s)
	return strings.Join(strings.Fields(s), " ")
}

func runeLen(s string) int {
	return len([]rune(s))
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
	if maxRunes <= 0 {
		return ""
	}
	r := []rune(s)
	if len(r) <= maxRunes {
		return s
	}
	if maxRunes == 1 {
		return "…"
	}
	return string(r[:maxRunes-1]) + "…"
}
