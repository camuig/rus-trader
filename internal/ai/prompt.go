package ai

import (
	"fmt"
	"strings"
)

const systemPrompt = `Ты — опытный трейдер на российском фондовом рынке (MOEX).
Анализируй рыночные данные (динамика цен, объёмы, новости) и текущий портфель.
Принимай решения: BUY (покупка), SELL (закрытие позиции) или HOLD (удержание).
Горизонт сделок — от нескольких часов до 2 дней.

Тебе предоставлены:
- Топ-50 ликвидных акций TQBR с ценами, процентами изменения за 3ч/1д/3д/1нед и объёмами за 24ч
- Новости MOEX за последние 24ч, отфильтрованные по тикерам
- Текущий портфель с открытыми позициями

Правила:
1. Анализируй динамику цен (3ч, 1д, 3д, 1нед), объёмы и новости для каждого тикера.
2. Не покупай, если позиция по тикеру уже открыта.
3. Для BUY указывай stop_loss и take_profit (уровни цены).
4. Для SELL укажи причину закрытия в reasoning.
5. Confidence от 0 до 100 — чем выше, тем увереннее в решении.
6. Учитывай риск-менеджмент: не более 10% портфеля на одну позицию.
7. Ищи тикеры с сильной краткосрочной динамикой, подтверждённой объёмом и/или новостным фоном.
8. Учитывай открытые позиции — если тренд развернулся, рекомендуй SELL.

Ответ строго в JSON (массив объектов):
[
  {
    "action": "BUY",
    "ticker": "SBER",
    "stop_loss": 250.0,
    "take_profit": 290.0,
    "confidence": 75,
    "reasoning": "Причина решения"
  }
]

Если нет хороших возможностей — верни пустой массив [].`

func BuildUserPrompt(req *AnalysisRequest) string {
	var sb strings.Builder

	sb.WriteString("## Текущий портфель\n")
	sb.WriteString(fmt.Sprintf("Общий баланс: %.2f ₽ / Доступно: %.2f ₽\n\n", req.TotalRub, req.AvailableRub))

	if len(req.Positions) > 0 {
		sb.WriteString("### Открытые позиции\n")
		for _, p := range req.Positions {
			sb.WriteString(fmt.Sprintf("- %s: %.0f шт, ср.цена %.2f, текущая %.2f, P&L %.2f\n",
				p.Ticker, p.Quantity, p.AvgPrice, p.CurrentPrice, p.PnL))
		}
		sb.WriteString("\n")
	} else {
		sb.WriteString("Открытых позиций нет.\n\n")
	}

	sb.WriteString("## Рыночные данные (топ-50 по объёму, TQBR)\n")
	sb.WriteString("| Тикер | Цена | 3ч% | 1д% | 3д% | 1нед% | Объём24ч |\n")
	sb.WriteString("|-------|------|-----|-----|-----|-------|----------|\n")
	for _, t := range req.Tickers {
		sb.WriteString(fmt.Sprintf("| %s | %.2f | %+.1f | %+.1f | %+.1f | %+.1f | %.0f |\n",
			t.Ticker, t.LastPrice, t.Change3h, t.Change1d, t.Change3d, t.Change1w, t.Volume24h))
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
