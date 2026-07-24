package guard

import (
	"fmt"
	"time"

	"github.com/camuig/rus-trader/internal/broker"
)

// Категории причин блокировки — для агрегированной диагностики.
const (
	BlockReasonCooldown     = "cooldown"
	BlockReasonRecentLoss   = "recent_loss"
	BlockReasonLosingStreak = "losing_streak"
	BlockReasonRSI          = "rsi_overbought"
	BlockReasonDowntrend    = "downtrend"
	BlockReasonLowATR       = "low_atr"
)

// PreScreenResult содержит тикеры, пропущенные к ИИ, карту причин для заблокированных
// и агрегированные счётчики по категориям (для диагностики жёсткости фильтров).
type PreScreenResult struct {
	Allowed     []broker.CandleSnapshot
	Blocked     map[string]string // ticker -> human-readable reason
	BlockCounts map[string]int    // category -> count
}

// PreScreenBuyable отсеивает не-позиционные тикеры, которые точно будут заблокированы
// детерминированными BUY-фильтрами (ATR%, RSI, uptrend, cooldown после SELL, recent_loss,
// losing_streak). Позиционные тикеры всегда проходят — они нужны Position Manager для SELL/HOLD.
//
// Цель — не тратить ограниченные слоты ИИ (MaxAnalysisTickers) на заведомо обречённые кандидаты.
func (g *TradeGuard) PreScreenBuyable(snapshots []broker.CandleSnapshot, positionTickers map[string]bool) PreScreenResult {
	cfg := g.config.Trading

	cooldown := time.Duration(cfg.CooldownMinutes) * time.Minute

	var recentLoss map[string]struct{}
	if cfg.RecentLossCooldownDays > 0 {
		if tickers, err := g.repo.GetRecentLossTickers(cfg.RecentLossCooldownDays); err == nil {
			recentLoss = make(map[string]struct{}, len(tickers))
			for _, t := range tickers {
				recentLoss[t] = struct{}{}
			}
		} else {
			g.logger.Error("prescreen: load recent loss tickers", "error", err)
		}
	}

	var losingStreak map[string]int
	if cfg.MaxLosingStreakPerTicker > 0 {
		if streaks, err := g.repo.GetLosingStreakTickers(cfg.MaxLosingStreakPerTicker, cfg.LosingStreakWindowDays); err == nil {
			losingStreak = streaks
		} else {
			g.logger.Error("prescreen: load losing streak tickers", "error", err)
		}
	}

	result := PreScreenResult{
		Allowed:     make([]broker.CandleSnapshot, 0, len(snapshots)),
		Blocked:     make(map[string]string),
		BlockCounts: make(map[string]int),
	}

	for _, snap := range snapshots {
		if positionTickers[snap.Ticker] {
			result.Allowed = append(result.Allowed, snap)
			continue
		}

		if category, reason := g.checkBuyCandidate(snap, cooldown, recentLoss, losingStreak); reason != "" {
			result.Blocked[snap.Ticker] = reason
			result.BlockCounts[category]++
			continue
		}

		result.Allowed = append(result.Allowed, snap)
	}

	return result
}

// checkBuyCandidate возвращает (category, human-readable reason) для первой сработавшей
// причины блокировки, либо ("", "") если тикер проходит все фильтры.
func (g *TradeGuard) checkBuyCandidate(
	snap broker.CandleSnapshot,
	cooldown time.Duration,
	recentLoss map[string]struct{},
	losingStreak map[string]int,
) (string, string) {
	cfg := g.config.Trading

	if cfg.CooldownMinutes > 0 {
		if lastSell, err := g.repo.GetLastSellTime(snap.Ticker); err == nil {
			if elapsed := time.Since(lastSell); elapsed < cooldown {
				remaining := cooldown - elapsed
				return BlockReasonCooldown, fmt.Sprintf("cooldown после продажи (осталось %d мин)", int(remaining.Minutes()))
			}
		}
	}

	if _, ok := recentLoss[snap.Ticker]; ok {
		return BlockReasonRecentLoss, fmt.Sprintf("был убыточным в последние %d дн.", cfg.RecentLossCooldownDays)
	}

	if cfg.MaxLosingStreakPerTicker > 0 {
		if streak, ok := losingStreak[snap.Ticker]; ok && streak >= cfg.MaxLosingStreakPerTicker {
			if cfg.LosingStreakWindowDays > 0 {
				return BlockReasonLosingStreak, fmt.Sprintf("%d убыточных подряд за %d дн. (лимит %d)", streak, cfg.LosingStreakWindowDays, cfg.MaxLosingStreakPerTicker)
			}
			return BlockReasonLosingStreak, fmt.Sprintf("%d убыточных подряд (лимит %d)", streak, cfg.MaxLosingStreakPerTicker)
		}
	}

	ind := snap.Indicators

	if ind.RSI14 > 80 {
		return BlockReasonRSI, fmt.Sprintf("RSI перекуплен (%.1f > 80)", ind.RSI14)
	}

	if cfg.RequireUptrend && ind.EMA9 > 0 && ind.EMA21 > 0 && ind.EMA9 < ind.EMA21 {
		return BlockReasonDowntrend, fmt.Sprintf("нисходящий тренд (EMA9=%.2f < EMA21=%.2f)", ind.EMA9, ind.EMA21)
	}

	if cfg.MinATRPct > 0 && ind.ATR14 > 0 && ind.EMA21 > 0 {
		atrPct := ind.ATR14 / ind.EMA21 * 100
		if atrPct < cfg.MinATRPct {
			return BlockReasonLowATR, fmt.Sprintf("ATR слишком низкий (%.2f%% < %.1f%%)", atrPct, cfg.MinATRPct)
		}
	}

	return "", ""
}

// CanOpenNewPositions возвращает (true, "") если в текущем цикле ещё разрешено открывать
// новые позиции. При false — screening agent можно полностью пропустить, т.к. любой его BUY
// всё равно будет отсечён. Position Manager продолжает работать независимо.
func (g *TradeGuard) CanOpenNewPositions() (bool, string) {
	cfg := g.config.Trading

	if cfg.NoLastHourBuy {
		now := time.Now().In(g.loc)
		totalMinutes := now.Hour()*60 + now.Minute()
		if totalMinutes >= 1070 { // 17:50 MSK
			return false, "запрет BUY в последний час торгов"
		}
	}

	if openCount, err := g.repo.CountOpenPositions(); err == nil && openCount >= cfg.MaxOpenPositions {
		return false, fmt.Sprintf("лимит открытых позиций (%d/%d)", openCount, cfg.MaxOpenPositions)
	}

	if dailyCount, err := g.repo.CountTodayTrades(); err == nil && dailyCount >= cfg.MaxDailyTrades {
		return false, fmt.Sprintf("лимит сделок за день (%d/%d)", dailyCount, cfg.MaxDailyTrades)
	}

	if cfg.MaxDailyLossRub > 0 {
		if pnl, err := g.repo.GetTodayPnL(); err == nil && pnl < -cfg.MaxDailyLossRub {
			return false, fmt.Sprintf("circuit breaker: дневной убыток %.0f ₽ превышает лимит -%.0f ₽", pnl, cfg.MaxDailyLossRub)
		}
	}

	return true, ""
}
