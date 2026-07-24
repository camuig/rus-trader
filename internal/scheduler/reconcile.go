package scheduler

import (
	"fmt"
	"strings"

	"github.com/camuig/rus-trader/internal/broker"
	"github.com/camuig/rus-trader/internal/storage"
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

// adoptOrphanPositions создаёт в БД записи для позиций, которые существуют у брокера,
// но отсутствуют в БД как open. Нужно после ручных покупок через приложение Т-Инвеста,
// рестарта в момент записи или восстановления из бэкапа — иначе позиция не видна в
// веб-интерфейсе и не управляется через Position Manager.
//
// SL/TP не восстанавливаются (их нет в портфеле брокера) — управление происходит
// через trailing stop и решения Position Manager.
//
// ВАЖНО: PortfolioInfo.Quantity — это количество акций, а Trade.Quantity — лотов.
// Конвертируем через размер лота инструмента.
func (s *Scheduler) adoptOrphanPositions(portfolio *broker.PortfolioInfo) {
	if portfolio == nil {
		return
	}

	for _, pos := range portfolio.Positions {
		if pos.Ticker == "" || pos.Quantity <= 0 {
			continue
		}

		lots := s.sharesToLots(pos)
		if lots <= 0 {
			s.logger.Error("adopt: failed to determine lot size, skipping",
				"ticker", pos.Ticker, "shares", pos.Quantity)
			continue
		}

		existing, err := s.repo.GetOpenTradeByTicker(pos.Ticker)
		if err == nil && existing != nil {
			// Одноразовая починка предыдущих некорректных adopt-записей,
			// где Quantity хранилось в акциях вместо лотов.
			if isAutoAdopted(existing.Reasoning) && existing.Quantity != lots {
				s.logger.Info("adopt: fixing prior record (shares→lots)",
					"ticker", pos.Ticker, "old_qty", existing.Quantity, "new_qty", lots)
				existing.Quantity = lots
				existing.Price = pos.AvgPrice
				if err := s.repo.UpdateTrade(existing); err != nil {
					s.logger.Error("adopt: update trade", "ticker", pos.Ticker, "error", err)
				}
			}
			continue
		}

		trade := &storage.Trade{
			Ticker:    pos.Ticker,
			Action:    "BUY",
			Price:     pos.AvgPrice,
			Quantity:  lots,
			Status:    "open",
			Reasoning: "auto-adopt: позиция найдена у брокера без записи в БД",
		}
		if err := s.repo.SaveTrade(trade); err != nil {
			s.logger.Error("adopt: save trade", "ticker", pos.Ticker, "error", err)
			continue
		}

		s.logger.Info("adopt: registered orphan broker position",
			"ticker", pos.Ticker, "price", pos.AvgPrice, "lots", lots, "shares", pos.Quantity)
		s.notifier.NotifyError("orphan adopt: "+pos.Ticker,
			fmt.Errorf("позиция усыновлена (вход %.4f, %d лот / %.0f шт)", pos.AvgPrice, lots, pos.Quantity))
	}
}

// sharesToLots конвертирует количество акций (из портфеля брокера) в лоты.
// Если InstrumentUID пуст или API не отвечает, возвращает 0.
func (s *Scheduler) sharesToLots(pos broker.PositionInfo) int64 {
	if pos.InstrumentUID == "" {
		return 0
	}
	lookup := s.lotSizeFn
	if lookup == nil {
		lookup = s.broker.GetLotSize
	}
	lotSize, err := lookup(pos.InstrumentUID)
	if err != nil || lotSize <= 0 {
		s.logger.Error("adopt: GetLotSize failed", "ticker", pos.Ticker, "error", err)
		return 0
	}
	return int64(pos.Quantity) / int64(lotSize)
}

func isAutoAdopted(reasoning string) bool {
	return strings.Contains(reasoning, "auto-adopt")
}
