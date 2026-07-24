package ai

import (
	"fmt"
	"strings"

	"github.com/camuig/rus-trader/internal/config"
)

// ScreeningSystemPrompt строит системный промпт скринера с реальными правилами риска
// из конфига — чтобы модель предлагала SL/TP в тех рамках, которые код всё равно
// принудительно применит, а не вслепую.
func ScreeningSystemPrompt(t config.TradingConfig) string {
	return fmt.Sprintf(`Роль: Аналитик-скринер MOEX. Горизонт сделки — свинг: 2-10 торговых дней.
Задача: Из предложенных тикеров отбери лучшие BUY-сетапы.

Ты НЕ управляешь позициями — только ищешь новые входы.

Как оценивать:
- Качество тренда и импульса (входим по тренду, не против него).
- Потенциал движения на несколько дней, а не только внутридневной импульс.
- Подтверждение объёмом (повышенный объём усиливает сигнал).
- Близость к уровням поддержки/сопротивления.
- Дивидендный фактор (предстоящая отсечка = катализатор роста).
- Новостной фон (позитив/негатив по тикеру и рынку).
- Уроки из прошлых сделок — не повторяй известные ошибки.

Правила риска (применяются кодом автоматически — предлагай SL/TP сразу в этих рамках):
- Размер позиции: до %.0f ₽, масштабируется от confidence (>=90 → 100%%, >=80 → 75%%, иначе 50%%).
- SL строго ниже входа, через ATR. Код расширит SL минимум до max(%.1f%%, %.1f×ATR%%) от входа и обрежет максимум до 6%%.
- TP минимум %.1f%% от входа; итоговое соотношение прибыль/риск будет не меньше %.1f — если предложишь меньше, TP растянут принудительно.
- Дневной circuit breaker: при убытке за день >= %.0f ₽ новые покупки блокируются.
- Совокупный риск открытых позиций Σ(вход−SL)×кол-во ограничен %.0f ₽.

Confidence:
  85-95: трендовый сетап + объём + катализатор.
  70-84: рабочий сетап, один фактор неопределённости.
  <70: не возвращай — будет отсеяно.

Формат: JSON массив BUY-решений. Пустой [] — если ни один тикер не проходит фильтр.
[{"action":"BUY","ticker":"SBER","stop_loss":250.0,"take_profit":290.0,"confidence":80,"reasoning":"Причина"}]
`,
		t.MaxPositionRub, t.MinStopLossPct, t.ATRStopLossMultiplier,
		t.MinTakeProfitPct, t.MinRiskRewardRatio, t.MaxDailyLossRub, t.MaxPortfolioRiskRub)
}

func BuildScreeningPrompt(req *ScreeningRequest, maxChars int) string {
	if maxChars <= 0 {
		maxChars = 16000
	}

	var sb strings.Builder

	if !req.CurrentTime.IsZero() {
		sb.WriteString(fmt.Sprintf("## Время: %s MSK\n\n", req.CurrentTime.Format("02.01.2006 15:04")))
	}

	if req.Market.IndexTicker != "" {
		sb.WriteString(fmt.Sprintf("## Фон рынка: %s: 1д %+.2f%%, 3д %+.2f%%, 1н %+.2f%% — %s\n\n",
			req.Market.IndexTicker, req.Market.ChangePct1d, req.Market.ChangePct3d, req.Market.ChangePct1w, req.Market.Regime))
	}

	if req.Stats.TradeCount7d > 0 {
		sb.WriteString("## Статистика 7 дней\n")
		sb.WriteString(fmt.Sprintf("Сделок: %d, Win rate: %.0f%%, P&L: %+.0f ₽\n",
			req.Stats.TradeCount7d, req.Stats.WinRate7d, req.Stats.TotalPnL7d))
		if req.Stats.LosingStreak >= 3 {
			sb.WriteString(fmt.Sprintf("⚠ %d убыточных подряд — повышай планку.\n", req.Stats.LosingStreak))
		}
		sb.WriteString("\n")
	}

	sb.WriteString(fmt.Sprintf("## Бюджет: %.0f ₽ доступно\n\n", req.AvailableRub))

	if len(req.Lessons) > 0 {
		sb.WriteString("## Уроки из последних сделок\n")
		for _, l := range req.Lessons {
			sb.WriteString("- ")
			sb.WriteString(l)
			sb.WriteString("\n")
		}
		sb.WriteString("\n")
	}

	if len(req.TodayTraded) > 0 {
		sb.WriteString("## Сегодня уже торговали (НЕ покупать): ")
		sb.WriteString(strings.Join(req.TodayTraded, ", "))
		sb.WriteString("\n\n")
	}

	sb.WriteString("## Тикеры\n")
	for _, f := range req.TickerFeatures {
		sb.WriteString(f)
		sb.WriteString("\n")
	}
	sb.WriteString("\n")

	// Similar patterns from vector memory
	if len(req.SimilarPatterns) > 0 {
		sb.WriteString("## Похожие исторические паттерны\n")
		for ticker, patterns := range req.SimilarPatterns {
			if len(patterns) == 0 {
				continue
			}
			var wins, losses int
			var winPnL, lossPnL float64
			for _, p := range patterns {
				if p.Outcome == "win" {
					wins++
					winPnL += p.PnL
				} else {
					losses++
					lossPnL += p.PnL
				}
			}
			total := wins + losses
			wr := 0.0
			if total > 0 {
				wr = float64(wins) / float64(total) * 100
			}
			sb.WriteString(fmt.Sprintf("%s: %d похожих — %d win", ticker, total, wins))
			if wins > 0 {
				sb.WriteString(fmt.Sprintf(" (+avg %.0f₽)", winPnL/float64(wins)))
			}
			sb.WriteString(fmt.Sprintf(", %d loss", losses))
			if losses > 0 {
				sb.WriteString(fmt.Sprintf(" (avg %.0f₽)", lossPnL/float64(losses)))
			}
			sb.WriteString(fmt.Sprintf(". WR %.0f%%.\n", wr))
			for i, p := range patterns {
				if i >= 2 {
					break
				}
				feat := p.Features
				if len([]rune(feat)) > 60 {
					feat = string([]rune(feat)[:59]) + "…"
				}
				sb.WriteString(fmt.Sprintf("  (%.2f) %s → %s %+.0f₽ за %.0fч\n",
					p.Similarity, feat, p.Outcome, p.PnL, p.HoldHours))
			}
		}
		sb.WriteString("\n")
	}

	if len(req.TickerNews) > 0 {
		sb.WriteString("## Новости по тикерам\n")
		for ticker, news := range req.TickerNews {
			for _, n := range news {
				sb.WriteString(fmt.Sprintf("- %s: %s\n", ticker, n))
			}
		}
		sb.WriteString("\n")
	}

	if len(req.GlobalNews) > 0 {
		sb.WriteString("## Общий фон\n")
		for _, n := range req.GlobalNews {
			sb.WriteString("- ")
			sb.WriteString(n)
			sb.WriteString("\n")
		}
		sb.WriteString("\n")
	}

	sb.WriteString("\nОтбери лучшие BUY-сетапы в JSON.")

	result := sb.String()
	if len([]rune(result)) > maxChars {
		r := []rune(result)
		result = string(r[:maxChars-1]) + "…"
	}
	return result
}
