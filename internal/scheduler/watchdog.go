package scheduler

import (
	"context"
	"fmt"
	"math"
	"time"

	"github.com/camuig/rus-trader/internal/broker"
	"github.com/camuig/rus-trader/internal/storage"
)

// RunStopWatchdog — быстрый цикл контроля SL/TP. Основной аналитический цикл ходит
// раз в 15 минут и закрывает пробитый стоп только решением AI, из-за чего фактический
// выход случается на 2-5% ниже стопа. Вотчдог каждые ~2 минуты сравнивает котировку
// открытых позиций с их SL/TP из БД и закрывает позицию немедленно, без AI.
func (s *Scheduler) RunStopWatchdog(ctx context.Context) {
	interval := s.config.StopWatchdogInterval()
	if interval <= 0 {
		s.logger.Info("stop watchdog disabled")
		return
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	s.logger.Info("stop watchdog started", "interval", interval.String())

	for {
		select {
		case <-ctx.Done():
			s.logger.Info("stop watchdog stopped")
			return
		case <-ticker.C:
			s.watchdogPass()
		}
	}
}

func (s *Scheduler) watchdogPass() {
	if !s.isWithinTradingHours() {
		return
	}

	trades, err := s.repo.GetOpenTrades()
	if err != nil {
		s.logger.Error("watchdog: get open trades", "error", err)
		return
	}

	for i := range trades {
		t := &trades[i]

		uid, err := s.broker.ResolveTickerToUID(t.Ticker)
		if err != nil {
			continue
		}
		if !s.broker.IsAPITradeAvailable(uid) {
			continue
		}

		price := s.broker.GetLastPrice(uid)
		reason, ok := watchdogAction(t, price, s.config.Trading.MaxPriceDeviationPct)
		if !ok {
			if reason != "" {
				s.logger.Warn("watchdog: "+reason, "ticker", t.Ticker, "price", price, "entry", t.Price)
			}
			continue
		}

		s.logger.Info("watchdog: forcing position close",
			"ticker", t.Ticker, "price", price, "sl", t.StopLossPrice, "tp", t.TakeProfitPrice)
		s.executor.ForceClose(t.Ticker, reason)
	}
}

// filterAnomalousSnapshots отбрасывает тикеры с заведомо битыми данными: котировка
// отсутствует или дневное изменение превышает max_price_deviation_pct (реальные
// движения ликвидных бумаг MOEX таких величин без планок не достигают, а вот
// песочница такие цены отдаёт регулярно).
func (s *Scheduler) filterAnomalousSnapshots(snaps []broker.CandleSnapshot) []broker.CandleSnapshot {
	filtered := snaps[:0]
	for _, snap := range snaps {
		if reason := snapshotAnomaly(snap, s.config.Trading.MaxPriceDeviationPct); reason != "" {
			s.logger.Warn("snapshot dropped: "+reason,
				"ticker", snap.Ticker, "last_price", snap.LastPrice, "day_open", snap.Period1d.Open)
			continue
		}
		filtered = append(filtered, snap)
	}
	return filtered
}

// snapshotAnomaly возвращает описание аномалии данных тикера или "" если данные вменяемы.
func snapshotAnomaly(snap broker.CandleSnapshot, maxDeviationPct float64) string {
	if snap.LastPrice <= 0 {
		return "no last price"
	}
	if maxDeviationPct <= 0 || snap.Period1d.Open <= 0 {
		return ""
	}
	if math.Abs(snap.LastPrice-snap.Period1d.Open)/snap.Period1d.Open*100 > maxDeviationPct {
		return "day change exceeds max_price_deviation_pct, likely bad data"
	}
	return ""
}

// watchdogAction решает, надо ли принудительно закрывать позицию по текущей котировке.
// Возвращает (причина, true) при необходимости закрытия; (описание аномалии, false),
// если котировка выглядит битой; ("", false), если делать ничего не нужно.
func watchdogAction(t *storage.Trade, price, maxDeviationPct float64) (string, bool) {
	if price <= 0 {
		return "", false
	}
	// Битая котировка (кейс EUTR: 0.23 при входе 29.85) не должна закрывать позицию
	// по мусорной цене — лучше пропустить проход и дождаться нормальных данных.
	if t.Price > 0 && maxDeviationPct > 0 &&
		math.Abs(price-t.Price)/t.Price*100 > maxDeviationPct {
		return "quote deviates too far from entry, skipping (bad data?)", false
	}

	switch {
	case t.StopLossPrice > 0 && price <= t.StopLossPrice:
		return fmt.Sprintf("стоп-вотчдог: цена %.2f пробила SL %.2f", price, t.StopLossPrice), true
	case t.TakeProfitPrice > 0 && price >= t.TakeProfitPrice:
		return fmt.Sprintf("стоп-вотчдог: цена %.2f достигла TP %.2f", price, t.TakeProfitPrice), true
	}
	return "", false
}
