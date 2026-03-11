package guard

import (
	"fmt"
	"sort"
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

type filterState struct {
	openPositionsKnown bool
	openPositions      int
	dailyBuysKnown     bool
	dailyBuys          int
	openTickersKnown   bool
	openTickers        map[string]struct{}
	sellClosable       map[string]struct{}
	soldThisCycle      map[string]struct{}
	boughtThisCycle    map[string]struct{}
}

func NewTradeGuard(repo *storage.Repository, cfg *config.Config, log *logger.Logger) *TradeGuard {
	return &TradeGuard{
		repo:   repo,
		config: cfg,
		logger: log,
	}
}

func (g *TradeGuard) Filter(decisions []ai.AIDecision) (allowed, blocked []BlockedDecision) {
	ordered := prioritizeDecisions(decisions)
	state := g.loadFilterState()

	for _, d := range ordered {
		if reason := g.check(d, &state); reason != "" {
			blocked = append(blocked, BlockedDecision{Decision: d, Reason: reason})
			g.logger.Info("decision blocked",
				"ticker", d.Ticker, "action", d.Action, "reason", reason)
		} else {
			allowed = append(allowed, BlockedDecision{Decision: d})
			state.apply(d)
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

func (g *TradeGuard) check(d ai.AIDecision, state *filterState) string {
	switch d.Action {
	case "BUY":
		return g.checkBuy(d, state)
	case "SELL":
		return g.checkSell(d, state)
	}
	return ""
}

func (g *TradeGuard) checkBuy(d ai.AIDecision, state *filterState) string {
	cfg := g.config.Trading

	if _, soldNow := state.soldThisCycle[d.Ticker]; soldNow {
		return fmt.Sprintf("cooldown после продажи (осталось %d мин)", cfg.CooldownMinutes)
	}

	// 1. Cooldown: after SELL, block BUY for cooldown_minutes
	if lastSell, err := g.repo.GetLastSellTime(d.Ticker); err == nil {
		cooldown := time.Duration(cfg.CooldownMinutes) * time.Minute
		if time.Since(lastSell) < cooldown {
			remaining := cooldown - time.Since(lastSell)
			return fmt.Sprintf("cooldown после продажи (осталось %d мин)", int(remaining.Minutes()))
		}
	}

	// 2. Position already open for ticker (unless closed in this cycle)
	if _, boughtNow := state.boughtThisCycle[d.Ticker]; boughtNow {
		return "позиция по тикеру уже открывается в этом цикле"
	}
	if _, soldNow := state.soldThisCycle[d.Ticker]; !soldNow {
		if state.openTickersKnown {
			if _, isOpen := state.openTickers[d.Ticker]; isOpen {
				return "позиция по тикеру уже открыта"
			}
		} else if openTrade, err := g.repo.GetOpenTradeByTicker(d.Ticker); err == nil && openTrade != nil {
			return "позиция по тикеру уже открыта"
		}
	}

	// 2. Max open positions
	if state.openPositionsKnown && state.openPositions >= cfg.MaxOpenPositions {
		return fmt.Sprintf("лимит открытых позиций (%d/%d)", state.openPositions, cfg.MaxOpenPositions)
	}

	// 3. Max daily trades
	if state.dailyBuysKnown && state.dailyBuys >= cfg.MaxDailyTrades {
		return fmt.Sprintf("лимит сделок за день (%d/%d)", state.dailyBuys, cfg.MaxDailyTrades)
	}

	return ""
}

func (g *TradeGuard) checkSell(d ai.AIDecision, state *filterState) string {
	cfg := g.config.Trading

	if _, soldNow := state.soldThisCycle[d.Ticker]; soldNow {
		return "позиция по тикеру уже закрывается в этом цикле"
	}

	// Min hold time
	if openTrade, err := g.repo.GetOpenTradeByTicker(d.Ticker); err == nil && openTrade != nil {
		minHold := time.Duration(cfg.MinHoldMinutes) * time.Minute
		held := time.Since(openTrade.CreatedAt)
		if held < minHold {
			remaining := minHold - held
			return fmt.Sprintf("мин. удержание позиции (осталось %d мин)", int(remaining.Minutes()))
		}
		state.sellClosable[d.Ticker] = struct{}{}
	}

	return ""
}

func prioritizeDecisions(decisions []ai.AIDecision) []ai.AIDecision {
	ordered := make([]ai.AIDecision, len(decisions))
	copy(ordered, decisions)
	sort.SliceStable(ordered, func(i, j int) bool {
		return actionPriority(ordered[i].Action) < actionPriority(ordered[j].Action)
	})
	return ordered
}

func actionPriority(action string) int {
	switch action {
	case "SELL":
		return 0
	case "HOLD":
		return 1
	case "BUY":
		return 2
	default:
		return 3
	}
}

func (g *TradeGuard) loadFilterState() filterState {
	state := filterState{
		openTickers:     make(map[string]struct{}),
		sellClosable:    make(map[string]struct{}),
		soldThisCycle:   make(map[string]struct{}),
		boughtThisCycle: make(map[string]struct{}),
	}

	if openTrades, err := g.repo.GetOpenTrades(); err == nil {
		state.openPositionsKnown = true
		state.openTickersKnown = true
		state.openPositions = len(openTrades)
		for _, t := range openTrades {
			state.openTickers[t.Ticker] = struct{}{}
		}
	} else if openCount, err := g.repo.CountOpenPositions(); err == nil {
		state.openPositionsKnown = true
		state.openPositions = openCount
	} else {
		g.logger.Error("load open positions count for guard", "error", err)
	}

	if dailyCount, err := g.repo.CountTodayTrades(); err == nil {
		state.dailyBuysKnown = true
		state.dailyBuys = dailyCount
	} else {
		g.logger.Error("load daily trades count for guard", "error", err)
	}

	return state
}

func (s *filterState) apply(d ai.AIDecision) {
	switch d.Action {
	case "SELL":
		if _, canClose := s.sellClosable[d.Ticker]; canClose {
			s.soldThisCycle[d.Ticker] = struct{}{}
			delete(s.boughtThisCycle, d.Ticker)
			delete(s.sellClosable, d.Ticker)
			if s.openTickersKnown {
				delete(s.openTickers, d.Ticker)
			}
			if s.openPositionsKnown && s.openPositions > 0 {
				s.openPositions--
			}
		}
	case "BUY":
		s.boughtThisCycle[d.Ticker] = struct{}{}
		if s.openTickersKnown {
			s.openTickers[d.Ticker] = struct{}{}
		}
		if s.openPositionsKnown {
			s.openPositions++
		}
		if s.dailyBuysKnown {
			s.dailyBuys++
		}
	}
}
