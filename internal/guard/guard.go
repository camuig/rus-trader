package guard

import (
	"fmt"
	"time"

	"github.com/camuig/rus-trader/internal/ai"
	"github.com/camuig/rus-trader/internal/config"
	"github.com/camuig/rus-trader/internal/logger"
	"github.com/camuig/rus-trader/internal/storage"
)

type BlockedDecision struct {
	Decision ai.AIDecision
	Reason   string
}

type TradeGuard struct {
	repo   *storage.Repository
	config *config.Config
	logger *logger.Logger
}

func NewTradeGuard(repo *storage.Repository, cfg *config.Config, log *logger.Logger) *TradeGuard {
	return &TradeGuard{
		repo:   repo,
		config: cfg,
		logger: log,
	}
}

func (g *TradeGuard) Filter(decisions []ai.AIDecision) (allowed, blocked []BlockedDecision) {
	for _, d := range decisions {
		if reason := g.check(d); reason != "" {
			blocked = append(blocked, BlockedDecision{Decision: d, Reason: reason})
			g.logger.Info("decision blocked",
				"ticker", d.Ticker, "action", d.Action, "reason", reason)
		} else {
			allowed = append(allowed, BlockedDecision{Decision: d})
		}
	}
	return allowed, blocked
}

func (g *TradeGuard) AllowedDecisions(decisions []ai.AIDecision) []ai.AIDecision {
	allowed, _ := g.Filter(decisions)
	result := make([]ai.AIDecision, len(allowed))
	for i, a := range allowed {
		result[i] = a.Decision
	}
	return result
}

func (g *TradeGuard) check(d ai.AIDecision) string {
	switch d.Action {
	case "BUY":
		return g.checkBuy(d)
	case "SELL":
		return g.checkSell(d)
	}
	return ""
}

func (g *TradeGuard) checkBuy(d ai.AIDecision) string {
	cfg := g.config.Trading

	// 1. Cooldown: after SELL, block BUY for cooldown_minutes
	if lastSell, err := g.repo.GetLastSellTime(d.Ticker); err == nil {
		cooldown := time.Duration(cfg.CooldownMinutes) * time.Minute
		if time.Since(lastSell) < cooldown {
			remaining := cooldown - time.Since(lastSell)
			return fmt.Sprintf("cooldown после продажи (осталось %d мин)", int(remaining.Minutes()))
		}
	}

	// 2. Max open positions
	if openCount, err := g.repo.CountOpenPositions(); err == nil {
		if openCount >= cfg.MaxOpenPositions {
			return fmt.Sprintf("лимит открытых позиций (%d/%d)", openCount, cfg.MaxOpenPositions)
		}
	}

	// 3. Max daily trades
	if dailyCount, err := g.repo.CountTodayTrades(); err == nil {
		if dailyCount >= cfg.MaxDailyTrades {
			return fmt.Sprintf("лимит сделок за день (%d/%d)", dailyCount, cfg.MaxDailyTrades)
		}
	}

	return ""
}

func (g *TradeGuard) checkSell(d ai.AIDecision) string {
	cfg := g.config.Trading

	// Min hold time
	if openTrade, err := g.repo.GetOpenTradeByTicker(d.Ticker); err == nil && openTrade != nil {
		minHold := time.Duration(cfg.MinHoldMinutes) * time.Minute
		held := time.Since(openTrade.CreatedAt)
		if held < minHold {
			remaining := minHold - held
			return fmt.Sprintf("мин. удержание позиции (осталось %d мин)", int(remaining.Minutes()))
		}
	}

	return ""
}
