package executor

import (
	"encoding/json"
	"fmt"

	"github.com/camuig/rus-trader/internal/ai"
	"github.com/camuig/rus-trader/internal/broker"
	"github.com/camuig/rus-trader/internal/config"
	"github.com/camuig/rus-trader/internal/logger"
	"github.com/camuig/rus-trader/internal/storage"
	"github.com/camuig/rus-trader/internal/telegram"
)

type Executor struct {
	broker   *broker.BrokerClient
	repo     *storage.Repository
	notifier *telegram.Notifier
	config   *config.Config
	logger   *logger.Logger
}

func NewExecutor(
	bc *broker.BrokerClient,
	repo *storage.Repository,
	notifier *telegram.Notifier,
	cfg *config.Config,
	log *logger.Logger,
) *Executor {
	return &Executor{
		broker:   bc,
		repo:     repo,
		notifier: notifier,
		config:   cfg,
		logger:   log,
	}
}

func (e *Executor) Execute(decisions []ai.AIDecision) {
	for _, d := range decisions {
		func() {
			defer func() {
				if r := recover(); r != nil {
					e.logger.Error("panic in executor", "ticker", d.Ticker, "panic", fmt.Sprint(r))
				}
			}()

			switch d.Action {
			case "BUY":
				e.executeBuy(d)
			case "SELL":
				e.executeSell(d)
			case "HOLD":
				e.logger.Info("HOLD decision", "ticker", d.Ticker, "reasoning", d.Reasoning)
			default:
				e.logger.Info("unknown action", "action", d.Action, "ticker", d.Ticker)
			}
		}()
	}
}

func (e *Executor) executeBuy(d ai.AIDecision) {
	if d.Confidence < e.config.Trading.MinConfidence {
		e.logger.Info("BUY skipped: low confidence",
			"ticker", d.Ticker, "confidence", d.Confidence, "min", e.config.Trading.MinConfidence)
		return
	}

	// Check if position already exists
	if existing, _ := e.repo.GetOpenTradeByTicker(d.Ticker); existing != nil {
		e.logger.Info("BUY skipped: position already open", "ticker", d.Ticker)
		return
	}

	// Check available balance
	availableRub, err := e.broker.GetAvailableRub()
	if err != nil {
		e.logger.Error("get available balance", "error", err)
		return
	}

	maxPosition := e.config.Trading.MaxPositionRub
	if maxPosition > availableRub {
		maxPosition = availableRub
	}

	// Resolve ticker to instrument UID
	instrumentUID, err := e.broker.ResolveTickerToUID(d.Ticker)
	if err != nil {
		e.logger.Error("resolve ticker", "ticker", d.Ticker, "error", err)
		return
	}

	// Calculate lots (using signal price as estimate)
	estimatedPrice := d.StopLoss * 1.05 // approximate current price from SL
	if estimatedPrice <= 0 {
		estimatedPrice = d.TakeProfit * 0.95
	}
	if estimatedPrice <= 0 {
		e.logger.Error("cannot estimate price for lots calculation", "ticker", d.Ticker)
		return
	}

	lots := e.broker.CalculateLots(instrumentUID, estimatedPrice, maxPosition)
	if lots < 1 {
		e.logger.Info("BUY skipped: insufficient balance for 1 lot", "ticker", d.Ticker)
		return
	}

	// Execute buy order
	result, err := e.broker.Buy(instrumentUID, lots)
	if err != nil {
		e.logger.Error("buy order failed", "ticker", d.Ticker, "error", err)
		e.notifier.NotifyError("BUY "+d.Ticker, err)
		return
	}

	executedPrice := result.ExecutedPrice

	// Calculate SL/TP prices
	slPrice := d.StopLoss
	tpPrice := d.TakeProfit
	if slPrice <= 0 {
		slPrice = executedPrice * (1 - e.config.Trading.DefaultStopLossPct/100)
	}
	if tpPrice <= 0 {
		tpPrice = executedPrice * (1 + e.config.Trading.DefaultTakeProfitPct/100)
	}

	// Place stop orders
	slOrderID, _ := e.broker.PlaceStopLoss(instrumentUID, result.ExecutedLots, slPrice)
	tpOrderID, _ := e.broker.PlaceTakeProfit(instrumentUID, result.ExecutedLots, tpPrice)

	// Save trade to DB
	trade := &storage.Trade{
		Ticker:            d.Ticker,
		Action:            "BUY",
		Price:             executedPrice,
		Quantity:          result.ExecutedLots,
		OrderID:           result.OrderID,
		StopLossPrice:     slPrice,
		TakeProfitPrice:   tpPrice,
		StopLossOrderID:   slOrderID,
		TakeProfitOrderID: tpOrderID,
		Status:            "open",
	}
	if err := e.repo.SaveTrade(trade); err != nil {
		e.logger.Error("save trade", "error", err)
	}

	e.notifier.NotifyBuy(d.Ticker, executedPrice, result.ExecutedLots, slPrice, tpPrice)
	e.logger.Info("BUY executed",
		"ticker", d.Ticker, "price", executedPrice, "lots", result.ExecutedLots,
		"sl", slPrice, "tp", tpPrice)
}

func (e *Executor) executeSell(d ai.AIDecision) {
	// Find open trade
	openTrade, err := e.repo.GetOpenTradeByTicker(d.Ticker)
	if err != nil {
		e.logger.Info("SELL skipped: no open position", "ticker", d.Ticker)
		return
	}

	instrumentUID, err := e.broker.ResolveTickerToUID(d.Ticker)
	if err != nil {
		e.logger.Error("resolve ticker for sell", "ticker", d.Ticker, "error", err)
		return
	}

	result, err := e.broker.Sell(instrumentUID, openTrade.Quantity)
	if err != nil {
		e.logger.Error("sell order failed", "ticker", d.Ticker, "error", err)
		e.notifier.NotifyError("SELL "+d.Ticker, err)
		return
	}

	// Cancel stop orders
	e.broker.CancelStopOrders(openTrade.StopLossOrderID, openTrade.TakeProfitOrderID)

	// Calculate PnL
	pnl := (result.ExecutedPrice - openTrade.Price) * float64(openTrade.Quantity)

	// Update trade in DB
	openTrade.PnL = pnl
	openTrade.Status = "closed"
	if err := e.repo.UpdateTrade(openTrade); err != nil {
		e.logger.Error("update trade", "error", err)
	}

	// Save sell trade record
	sellTrade := &storage.Trade{
		Ticker:   d.Ticker,
		Action:   "SELL",
		Price:    result.ExecutedPrice,
		Quantity: result.ExecutedLots,
		OrderID:  result.OrderID,
		PnL:      pnl,
		Status:   "closed",
	}
	if err := e.repo.SaveTrade(sellTrade); err != nil {
		e.logger.Error("save sell trade", "error", err)
	}

	e.notifier.NotifySell(d.Ticker, result.ExecutedPrice, result.ExecutedLots, pnl)
	e.logger.Info("SELL executed",
		"ticker", d.Ticker, "price", result.ExecutedPrice, "lots", result.ExecutedLots, "pnl", pnl)
}

func DecisionsToJSON(decisions []ai.AIDecision) string {
	data, err := json.Marshal(decisions)
	if err != nil {
		return "[]"
	}
	return string(data)
}
