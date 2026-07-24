package ai

import (
	"fmt"
	"strings"
	"time"
)

const systemPrompt = `Роль: Профессиональный дейтрейдер MOEX (горизонт 1-3 дня).
Задача: Анализируй технические индикаторы, OHLCV, объемы и новости и принимай решения BUY / SELL / HOLD.

Принцип работы: принимай решения на основании данных. Торговля — это работа, а не ожидание идеала. Находи лучшие доступные сетапы и ставь их с адекватным риском. Если в данных действительно нет ни одного кандидата — можно вернуть [], но это должно быть обоснованное решение, а не дефолт.

Как рассуждать:

1) Управление открытыми позициями:
   - HOLD — разумный дефолт для работающей позиции.
   - Не закрывай из-за шума (±2% — это норма MOEX), особенно если позиция открыта < 4 часов.
   - SELL оправдан, когда: (а) цена подошла вплотную к SL и структура сломана, (б) появился сильный фундаментальный негатив, (в) позиция >2 дней без прогресса к TP.
   - Прибыльные трендовые позиции держи до TP или до разворота тренда. Не закрывай ради "ротации".

2) Поиск BUY-сетапов — ищи качественные, но не придирайся к деталям:
   - Приоритетный сетап: восходящий тренд (EMA9 > EMA21) + подтверждение объёмом (RelVol > 1.2) + цена в нижней половине дневного диапазона или у поддержки.
   - Альтернативный сетап: отскок от сильной поддержки (RSI < 35, цена в 0.5-1 ATR от Support) при стабилизации объёма.
   - Альтернативный сетап: пробой сопротивления на повышенном объёме (RelVol > 1.5).
   - Учитывай новости по тикеру и общий фон. Явный негатив → не бери. Явный позитив усиливает сигнал.
   - Избегай "ловли ножа": 3+ дня стремительного падения без признаков разворота — не лучшая идея, но лёгкая коррекция к EMA21 в общем тренде — ок.
   - Не открывай позицию, если тикер уже есть в портфеле или был проторгован сегодня.

3) Уровни SL / TP:
   - SL ВСЕГДА строго НИЖЕ цены входа. SL >= entry — критическая ошибка.
   - Рассчитывай SL от ATR: SL ≈ entry − (1.5 … 2.0) × ATR, но не ближе, чем за ближайшую локальную поддержку с запасом.
   - TP ставь с учётом Resistance и ATR: реалистичный TP обычно 1.5-3 × (entry − SL).
   - Предложи свои SL/TP — система при необходимости расширит их до безопасных порогов, так что не занижай из страха.

4) Confidence:
   - 85-95: трендовый сетап с подтверждением по объёму и новостям.
   - 70-84: рабочий сетап, но есть один фактор неопределённости.
   - < 70: не возвращай BUY — это будет отсеяно.

5) Диагностика рынка:
   - Учитывай статистику за 7 дней и последние закрытые сделки — не повторяй одинаковые ошибки на тех же тикерах.
   - При серии убытков (≥3) — повышай планку качества, но не отказывайся от торговли полностью.

Формат ответа — СТРОГО JSON-массив (без markdown, без текста вокруг):
[
  {
    "action": "BUY",
    "ticker": "SBER",
    "stop_loss": 250.0,
    "take_profit": 290.0,
    "confidence": 80,
    "reasoning": "Краткая причина в 1 предложении"
  }
]

Важно:
- Ответ — ТОЛЬКО массив объектов, ничего другого.
- reasoning: одно короткое предложение по делу.
- Пустой массив [] допустим, если ни один тикер не проходит даже базовый фильтр качества. Но не используй [] как безопасный дефолт — это упущенная возможность.
`

func BuildUserPrompt(req *AnalysisRequest, todayTraded []string, limits PromptLimits) string {
	limits = normalizePromptLimits(limits)

	tail := "\nПроанализируй и выдай решения в JSON."
	bodyLimit := limits.MaxChars - runeLen(tail)
	if bodyLimit < 0 {
		bodyLimit = limits.MaxChars
	}

	builder := &cappedBuilder{maxRunes: bodyLimit}

	// Current time context
	if !req.CurrentTime.IsZero() {
		builder.WriteString(fmt.Sprintf("## Текущее время: %s MSK\n\n", req.CurrentTime.Format("02.01.2006 15:04")))
	}

	// Broad market context (IMOEX) — помогает модели понять режим рынка
	if req.Market.IndexTicker != "" {
		builder.WriteString("## Фон рынка\n")
		builder.WriteString(fmt.Sprintf("%s: 1д %+.2f%%, 3д %+.2f%%, 1н %+.2f%%",
			req.Market.IndexTicker, req.Market.ChangePct1d, req.Market.ChangePct3d, req.Market.ChangePct1w))
		if req.Market.Regime != "" {
			builder.WriteString(fmt.Sprintf(" — режим: %s", req.Market.Regime))
		}
		builder.WriteString("\n\n")
	}

	// Performance stats
	if req.Stats.TradeCount7d > 0 {
		builder.WriteString("## Статистика за 7 дней\n")
		builder.WriteString(fmt.Sprintf("Сделок: %d, Win rate: %.0f%%, P&L: %+.2f ₽\n",
			req.Stats.TradeCount7d, req.Stats.WinRate7d, req.Stats.TotalPnL7d))
		builder.WriteString(fmt.Sprintf("Ср. прибыль: +%.2f ₽, Ср. убыток: %.2f ₽\n",
			req.Stats.AvgProfit, req.Stats.AvgLoss))
		if len(req.Stats.WorstTickers) > 0 {
			builder.WriteString(fmt.Sprintf("Худшие тикеры: %s\n", strings.Join(req.Stats.WorstTickers, ", ")))
		}
		if req.Stats.LosingStreak >= 3 {
			builder.WriteString(fmt.Sprintf("\n⚠ ВНИМАНИЕ: %d убыточных сделок подряд! Будь СВЕРХ-осторожен. Лучше вернуть [] чем потерять ещё.\n", req.Stats.LosingStreak))
		}
		builder.WriteString("\n")
	}

	builder.WriteString("## Текущий портфель\n")
	builder.WriteString(fmt.Sprintf("Общий баланс: %.2f ₽ / Доступно: %.2f ₽\n", req.TotalRub, req.AvailableRub))
	if req.MaxOpenPositions > 0 {
		builder.WriteString(fmt.Sprintf("Открыто позиций: %d / Лимит: %d", len(req.Positions), req.MaxOpenPositions))
		if len(req.Positions) >= req.MaxOpenPositions {
			builder.WriteString(" ⚠ ЛИМИТ ДОСТИГНУТ — для новой покупки нужно сначала закрыть позицию")
		}
		builder.WriteString("\n")
	}
	builder.WriteString("\n")

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

	// Technical indicators section
	builder.WriteString("## Индикаторы\n")
	builder.WriteString("Ticker|RSI14|EMA9|EMA21|ATR14|RelVol|Support|Resist\n")
	for _, t := range req.Tickers {
		ind := t.Indicators
		builder.WriteString(fmt.Sprintf("%s|%.1f|%.2f|%.2f|%.2f|%.1fx|%.2f|%.2f\n",
			t.Ticker, ind.RSI14, ind.EMA9, ind.EMA21, ind.ATR14,
			ind.RelVolume, ind.Support, ind.Resistance))
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
		l.MaxChars = 24000
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
