package scheduler

import (
	"fmt"

	"github.com/camuig/rus-trader/internal/broker"
)

// reconcileOrphanTrades closes trades that are "open" in DB but whose ticker is
// absent from the broker's real portfolio. This handles cases where a stop order
// was executed at the broker level but the bot missed the update (e.g. inverted SL,
// app restart, network issue).
func (s *Scheduler) reconcileOrphanTrades(portfolio *broker.PortfolioInfo) {
	openTrades, err := s.repo.GetOpenTrades()
	if err != nil || len(openTrades) == 0 {
		return
	}

	// Build set of tickers that actually exist at broker
	brokerTickers := make(map[string]float64, len(portfolio.Positions))
	for _, pos := range portfolio.Positions {
		if pos.Ticker != "" && pos.Quantity > 0 {
			brokerTickers[pos.Ticker] = pos.CurrentPrice
		}
	}

	for _, t := range openTrades {
		if _, exists := brokerTickers[t.Ticker]; exists {
			continue
		}
		// Trade is "open" in DB but not at broker — close it.
		// We can't know the exact exit price, set PnL=0 and log.
		s.logger.Info("reconcile: closing stale orphan trade (not in broker portfolio)",
			"ticker", t.Ticker, "trade_id", t.ID, "opened", t.CreatedAt.Format("02.01 15:04"))
		t.Status = "closed"
		t.PnL = 0
		t.Reasoning = "auto-reconcile: позиция не найдена у брокера"
		if err := s.repo.UpdateTrade(&t); err != nil {
			s.logger.Error("reconcile: update trade", "error", err)
		}
		s.notifier.NotifyError("orphan reconcile: "+t.Ticker, fmt.Errorf("закрыта stale позиция (вход %.2f, %d шт, открыта %s)", t.Price, t.Quantity, t.CreatedAt.Format("02.01 15:04")))
	}
}
