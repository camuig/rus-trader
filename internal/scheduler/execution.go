package scheduler

import (
	"encoding/json"
	"fmt"

	"github.com/camuig/rus-trader/internal/ai"
	"github.com/camuig/rus-trader/internal/broker"
	"github.com/camuig/rus-trader/internal/executor"
	"github.com/camuig/rus-trader/internal/indicators"
	"github.com/camuig/rus-trader/internal/storage"
)

// applyGuardAndExecute runs guard filter, executes allowed decisions, saves logs.
func (s *Scheduler) applyGuardAndExecute(state *cycleState) {
	indicatorsMap := make(map[string]indicators.Indicators, len(state.snapshots))
	for _, snap := range state.snapshots {
		indicatorsMap[snap.Ticker] = snap.Indicators
	}
	s.guard.SetIndicators(indicatorsMap)
	s.executor.SetIndicators(indicatorsMap)

	allowed, blocked := s.guard.Filter(state.decisions)
	for _, b := range blocked {
		s.logger.Info("decision BLOCKED by guard",
			"ticker", b.Decision.Ticker, "action", b.Decision.Action,
			"reason", b.Reason, "confidence", b.Decision.Confidence)
		s.notifier.NotifyBlocked(b.Decision.Ticker, b.Decision.Action, b.Reason)
	}

	allowedDecisions := make([]ai.AIDecision, len(allowed))
	for i, a := range allowed {
		allowedDecisions[i] = a.Decision
	}
	s.logger.Info("guard filter applied", "allowed", len(allowedDecisions), "blocked", len(blocked))

	if len(allowedDecisions) == 0 && len(state.decisions) > 0 {
		s.logger.Info("all decisions were blocked or HOLD — no trades will be executed")
	}

	// Trailing stops
	if s.config.Trading.TrailingStopEnabled {
		s.updateTrailingStops(state.portfolio)
	}

	// Execute (снапшот портфеля нужен для sanity-check qty в executeSell)
	s.executor.SetPortfolio(state.portfolio)
	s.executor.Execute(allowedDecisions)

	// Save logs + snapshots
	s.saveAnalysisLog(len(state.tradableTickers), state.rawResponse, executor.DecisionsToJSON(state.decisions), nil)
	s.savePortfolioSnapshot(state.portfolio)
	s.saveCycleSnapshot(state)

	s.logger.Info("analysis cycle completed")
}

func (s *Scheduler) updateTrailingStops(portfolio *broker.PortfolioInfo) {
	openTrades, err := s.repo.GetOpenTrades()
	if err != nil {
		s.logger.Error("trailing stop: get open trades", "error", err)
		return
	}

	cfg := s.config.Trading
	breakevenPct := cfg.TrailingBreakevenPct / 100
	lockProfitPct := cfg.TrailingLockProfitPct / 100

	for _, trade := range openTrades {
		if trade.TakeProfitPrice <= 0 || trade.StopLossPrice <= 0 || trade.Price <= 0 {
			continue
		}

		var currentPrice float64
		for _, pos := range portfolio.Positions {
			if pos.Ticker == trade.Ticker {
				currentPrice = pos.CurrentPrice
				break
			}
		}
		if currentPrice <= 0 {
			continue
		}

		tpDistance := trade.TakeProfitPrice - trade.Price
		if tpDistance <= 0 {
			continue
		}

		progress := (currentPrice - trade.Price) / tpDistance
		var newSL float64

		if progress >= lockProfitPct {
			profit := currentPrice - trade.Price
			newSL = trade.Price + profit*0.5
		} else if progress >= breakevenPct {
			newSL = trade.Price * 1.001
		}

		if newSL <= 0 || newSL <= trade.StopLossPrice {
			continue
		}

		s.logger.Info("trailing stop: updating SL",
			"ticker", trade.Ticker, "oldSL", trade.StopLossPrice,
			"newSL", newSL, "progress", fmt.Sprintf("%.0f%%", progress*100))

		instrumentUID, err := s.broker.ResolveTickerToUID(trade.Ticker)
		if err != nil {
			s.logger.Error("trailing stop: resolve ticker", "ticker", trade.Ticker, "error", err)
			continue
		}

		if trade.StopLossOrderID != "" {
			s.broker.CancelStopOrders(trade.StopLossOrderID, "")
		}

		newSLOrderID, err := s.broker.PlaceStopLoss(instrumentUID, trade.Quantity, newSL)
		if err != nil {
			s.logger.Error("trailing stop: place new SL", "ticker", trade.Ticker, "error", err)
			continue
		}

		trade.StopLossPrice = newSL
		trade.StopLossOrderID = newSLOrderID
		if err := s.repo.UpdateTrade(&trade); err != nil {
			s.logger.Error("trailing stop: update trade", "error", err)
		}
	}
}

// saveCycleSnapshot persists the full cycle state for future backtesting.
func (s *Scheduler) saveCycleSnapshot(state *cycleState) {
	if state == nil || state.portfolio == nil {
		return
	}

	snapshotsJSON, _ := json.Marshal(state.allSnapshots)
	featJSON, _ := json.Marshal(state.featuresMap)
	portfolioJSON, _ := json.Marshal(state.portfolio)
	decisionsJSON := executor.DecisionsToJSON(state.decisions)
	newsData := map[string]interface{}{
		"ticker_news": state.tickerNews,
		"global_news": state.globalNews,
	}
	newsJSON, _ := json.Marshal(newsData)

	snap := &storage.CycleSnapshot{
		SnapshotsJSON: string(snapshotsJSON),
		FeaturesJSON:  string(featJSON),
		PortfolioJSON: string(portfolioJSON),
		DecisionsJSON: decisionsJSON,
		NewsJSON:      string(newsJSON),
		MarketRegime:  state.marketCtx.Regime,
	}
	if err := s.repo.SaveCycleSnapshot(snap); err != nil {
		s.logger.Error("save cycle snapshot", "error", err)
	}
}
