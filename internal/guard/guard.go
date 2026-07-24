package guard

import (
	"fmt"
	"sort"
	"time"

	"github.com/camuig/rus-trader/internal/ai"
	"github.com/camuig/rus-trader/internal/config"
	"github.com/camuig/rus-trader/internal/indicators"
	"github.com/camuig/rus-trader/internal/logger"
	"github.com/camuig/rus-trader/internal/storage"
)

type BlockedDecision struct {
	Decision ai.AIDecision
	Reason   string
}

type TradeGuard struct {
	repo       *storage.Repository
	config     *config.Config
	logger     *logger.Logger
	indicators map[string]indicators.Indicators // ticker -> indicators
	loc        *time.Location                   // MSK timezone

	// lotSizeFn возвращает размер лота по тикеру (для перевода Trade.Quantity из лотов
	// в акции при расчёте риска). nil или ошибка резолва → считаем лот = 1.
	lotSizeFn func(ticker string) int64
}

type filterState struct {
	openPositionsKnown   bool
	openPositions        int
	dailyBuysKnown       bool
	dailyBuys            int
	openTickersKnown     bool
	openTickers          map[string]struct{}
	sellClosable         map[string]struct{}
	soldThisCycle        map[string]struct{}
	boughtThisCycle      map[string]struct{}
	dailyPnL             float64
	dailyPnLKnown        bool
	recentLossTickers    map[string]struct{}
	losingStreakByTicker map[string]int
	openRiskKnown        bool
	openRiskRub          float64 // Σ(вход−SL)×акций по открытым позициям
}

func NewTradeGuard(repo *storage.Repository, cfg *config.Config, log *logger.Logger) *TradeGuard {
	return &TradeGuard{
		repo:       repo,
		config:     cfg,
		logger:     log,
		indicators: make(map[string]indicators.Indicators),
		loc:        cfg.MOEXLocation(),
	}
}

// SetIndicators sets technical indicators for use in pre-validation.
func (g *TradeGuard) SetIndicators(ind map[string]indicators.Indicators) {
	g.indicators = ind
}

// SetLotSizeFn задаёт lookup размера лота по тикеру (см. TradeGuard.lotSizeFn).
func (g *TradeGuard) SetLotSizeFn(fn func(ticker string) int64) {
	g.lotSizeFn = fn
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

	// 4. Circuit breaker: max daily loss
	if cfg.MaxDailyLossRub > 0 && state.dailyPnLKnown && state.dailyPnL < -cfg.MaxDailyLossRub {
		return fmt.Sprintf("circuit breaker: дневной убыток %.0f ₽ превышает лимит -%.0f ₽", state.dailyPnL, cfg.MaxDailyLossRub)
	}

	// 5. Recent loss cooldown: block tickers with losses in last N days
	if _, recentLoss := state.recentLossTickers[d.Ticker]; recentLoss {
		return fmt.Sprintf("тикер %s был убыточным в последние %d дн.", d.Ticker, cfg.RecentLossCooldownDays)
	}

	// 5b. Per-ticker losing streak within configured time window
	if cfg.MaxLosingStreakPerTicker > 0 {
		if streak, ok := state.losingStreakByTicker[d.Ticker]; ok && streak >= cfg.MaxLosingStreakPerTicker {
			window := cfg.LosingStreakWindowDays
			if window > 0 {
				return fmt.Sprintf("тикер %s: %d убыточных сделок подряд за %d дн. (лимит %d)", d.Ticker, streak, window, cfg.MaxLosingStreakPerTicker)
			}
			return fmt.Sprintf("тикер %s: %d убыточных сделок подряд (лимит %d)", d.Ticker, streak, cfg.MaxLosingStreakPerTicker)
		}
	}

	// 6. Pre-validation: RSI overbought check
	if ind, ok := g.indicators[d.Ticker]; ok {
		if ind.RSI14 > 80 {
			return fmt.Sprintf("RSI перекуплен (%.1f > 80)", ind.RSI14)
		}

		// Block BUY against downtrend (EMA9 < EMA21)
		if cfg.RequireUptrend && ind.EMA9 > 0 && ind.EMA21 > 0 && ind.EMA9 < ind.EMA21 {
			return fmt.Sprintf("нисходящий тренд (EMA9=%.2f < EMA21=%.2f)", ind.EMA9, ind.EMA21)
		}

		// Block BUY when ATR too low (insufficient volatility for targets)
		if cfg.MinATRPct > 0 && ind.ATR14 > 0 && ind.EMA21 > 0 {
			atrPct := ind.ATR14 / ind.EMA21 * 100
			if atrPct < cfg.MinATRPct {
				return fmt.Sprintf("ATR слишком низкий (%.2f%% < %.1f%%)", atrPct, cfg.MinATRPct)
			}
		}
	}

	// 7. Pre-validation: no BUY in last hour of trading
	if cfg.NoLastHourBuy {
		now := time.Now().In(g.loc)
		totalMinutes := now.Hour()*60 + now.Minute()
		if totalMinutes >= 1070 { // 17:50 MSK
			return "запрет BUY в последний час торгов"
		}
	}

	// 8. Лимит совокупного риска портфеля: Σ(вход−SL)×акций по открытым позициям
	// плюс оценка риска нового входа не должны превышать max_portfolio_risk_rub.
	if cfg.MaxPortfolioRiskRub > 0 && state.openRiskKnown {
		newRisk := g.estimateNewTradeRisk(d)
		if state.openRiskRub+newRisk > cfg.MaxPortfolioRiskRub {
			return fmt.Sprintf("лимит совокупного риска портфеля (открыто %.0f ₽ + новый %.0f ₽ > %.0f ₽)",
				state.openRiskRub, newRisk, cfg.MaxPortfolioRiskRub)
		}
		// Это последняя проверка: раз решение проходит, учитываем его риск для
		// последующих BUY этого же цикла.
		state.openRiskRub += newRisk
	}

	return ""
}

// estimateNewTradeRisk оценивает рублёвый риск нового BUY: размер позиции
// (с масштабированием по confidence, как в executor) × SL-дистанция в процентах.
// Точной цены входа ещё нет — прокси служит EMA9; без неё берётся дефолтный SL%.
func (g *TradeGuard) estimateNewTradeRisk(d ai.AIDecision) float64 {
	cfg := g.config.Trading

	posRub := cfg.MaxPositionRub
	switch {
	case d.Confidence >= 90:
	case d.Confidence >= 80:
		posRub *= 0.75
	default:
		posRub *= 0.50
	}

	slPct := cfg.DefaultStopLossPct
	if ind, ok := g.indicators[d.Ticker]; ok && ind.EMA9 > 0 && d.StopLoss > 0 && d.StopLoss < ind.EMA9 {
		slPct = (ind.EMA9 - d.StopLoss) / ind.EMA9 * 100
	}
	// Те же границы, что применит executor: floor min_stop_loss_pct, кэп 6%.
	if slPct < cfg.MinStopLossPct {
		slPct = cfg.MinStopLossPct
	}
	if slPct > 6.0 {
		slPct = 6.0
	}

	return posRub * slPct / 100
}

// openPortfolioRisk суммирует рублёвый риск открытых позиций: (вход−SL)×акций.
// Позиции без SL или с SL выше входа риском не считаются (их закроет вотчдог/reconcile).
func openPortfolioRisk(trades []storage.Trade, lotSizeFn func(string) int64) float64 {
	var total float64
	for _, t := range trades {
		if t.StopLossPrice <= 0 || t.Price <= t.StopLossPrice {
			continue
		}
		lotSize := int64(1)
		if lotSizeFn != nil {
			if ls := lotSizeFn(t.Ticker); ls > 0 {
				lotSize = ls
			}
		}
		total += (t.Price - t.StopLossPrice) * float64(t.Quantity*lotSize)
	}
	return total
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
		openTickers:       make(map[string]struct{}),
		sellClosable:      make(map[string]struct{}),
		soldThisCycle:     make(map[string]struct{}),
		boughtThisCycle:   make(map[string]struct{}),
		recentLossTickers: make(map[string]struct{}),
	}

	if openTrades, err := g.repo.GetOpenTrades(); err == nil {
		state.openPositionsKnown = true
		state.openTickersKnown = true
		state.openPositions = len(openTrades)
		for _, t := range openTrades {
			state.openTickers[t.Ticker] = struct{}{}
		}
		state.openRiskKnown = true
		state.openRiskRub = openPortfolioRisk(openTrades, g.lotSizeFn)
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

	// Circuit breaker: load today's P&L
	if pnl, err := g.repo.GetTodayPnL(); err == nil {
		state.dailyPnL = pnl
		state.dailyPnLKnown = true
	} else {
		g.logger.Error("load daily pnl for guard", "error", err)
	}

	// Recent loss tickers
	if days := g.config.Trading.RecentLossCooldownDays; days > 0 {
		if tickers, err := g.repo.GetRecentLossTickers(days); err == nil {
			for _, t := range tickers {
				state.recentLossTickers[t] = struct{}{}
			}
		} else {
			g.logger.Error("load recent loss tickers for guard", "error", err)
		}
	}

	// Per-ticker losing streak (within configurable window — prevents permanent blocks)
	if minStreak := g.config.Trading.MaxLosingStreakPerTicker; minStreak > 0 {
		window := g.config.Trading.LosingStreakWindowDays
		if streaks, err := g.repo.GetLosingStreakTickers(minStreak, window); err == nil {
			state.losingStreakByTicker = streaks
		} else {
			g.logger.Error("load losing streak tickers for guard", "error", err)
		}
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
