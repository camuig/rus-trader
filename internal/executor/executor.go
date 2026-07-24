package executor

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/camuig/rus-trader/internal/ai"
	"github.com/camuig/rus-trader/internal/broker"
	"github.com/camuig/rus-trader/internal/config"
	"github.com/camuig/rus-trader/internal/indicators"
	"github.com/camuig/rus-trader/internal/journal"
	"github.com/camuig/rus-trader/internal/logger"
	"github.com/camuig/rus-trader/internal/storage"
	"github.com/camuig/rus-trader/internal/telegram"
)

type Executor struct {
	broker      *broker.BrokerClient
	repo        *storage.Repository
	notifier    *telegram.Notifier
	config      *config.Config
	logger      *logger.Logger
	indicators  map[string]indicators.Indicators
	journal     *journal.Journal
	features    map[string]string // ticker → current textual features
	vectorStore interface {
		SaveEntry(ctx context.Context, tradeID uint, ticker, features string) error
		UpdateOutcome(tradeID uint, outcome string, pnl, holdHours float64) error
	}

	// SELL-cooldown: после неудачной попытки SELL по тикеру не повторять её какое-то время.
	// Защищает от спама notify при precondition-ошибках брокера (см. T-Invest 90001).
	failedSellAt map[string]time.Time

	// Тикеры, по которым уже отправили уведомление о недоступности торговли через API
	// (T-Invest 30052). Чтобы не спамить телегу одинаковой ошибкой каждый цикл —
	// уведомляем один раз на тикер за жизнь процесса.
	apiForbiddenNotified map[string]bool

	// Снимок портфеля от брокера для текущего цикла — используется как sanity-check
	// quantity перед SELL (сравнение БД ↔ реальная позиция).
	currentPortfolio *broker.PortfolioInfo
}

// sellFailureCooldown — время после неудачного SELL, в течение которого повторные попытки
// продажи этого же тикера пропускаются. Не делается параметром конфигурации намеренно:
// это инженерная защита от шторма ошибок, а не торговая настройка.
const sellFailureCooldown = 30 * time.Minute

// SetIndicators passes current indicators map to executor for ATR-based SL sizing.
func (e *Executor) SetIndicators(m map[string]indicators.Indicators) {
	e.indicators = m
}

// SetJournal sets the journal for recording trade outcomes.
func (e *Executor) SetJournal(j *journal.Journal) {
	e.journal = j
}

// SetFeatures sets current textual features for saving on BUY.
func (e *Executor) SetFeatures(f map[string]string) {
	e.features = f
}

// SetVectorStore sets the vector store for saving/updating pattern embeddings.
func (e *Executor) SetVectorStore(vs interface {
	SaveEntry(ctx context.Context, tradeID uint, ticker, features string) error
	UpdateOutcome(tradeID uint, outcome string, pnl, holdHours float64) error
}) {
	e.vectorStore = vs
}

func NewExecutor(
	bc *broker.BrokerClient,
	repo *storage.Repository,
	notifier *telegram.Notifier,
	cfg *config.Config,
	log *logger.Logger,
) *Executor {
	return &Executor{
		broker:               bc,
		repo:                 repo,
		notifier:             notifier,
		config:               cfg,
		logger:               log,
		failedSellAt:         make(map[string]time.Time),
		apiForbiddenNotified: make(map[string]bool),
	}
}

// SetPortfolio passes текущий снапшот портфеля для sanity-check'ов перед ордерами.
func (e *Executor) SetPortfolio(p *broker.PortfolioInfo) {
	e.currentPortfolio = p
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

// ForceClose закрывает открытую позицию в обход AI — используется стоп-вотчдогом,
// когда цена пробила SL или достигла TP между аналитическими циклами.
func (e *Executor) ForceClose(ticker, reasoning string) {
	defer func() {
		if r := recover(); r != nil {
			e.logger.Error("panic in ForceClose", "ticker", ticker, "panic", fmt.Sprint(r))
		}
	}()
	e.executeSell(ai.AIDecision{Ticker: ticker, Action: "SELL", Confidence: 100, Reasoning: reasoning})
}

// lotSizeOrOne возвращает размер лота инструмента; при ошибке — 1, чтобы расчёты
// объёма/PnL хотя бы не падали (для большинства ликвидных бумаг лот и есть 1).
func (e *Executor) lotSizeOrOne(ticker, instrumentUID string) int64 {
	lotSize, err := e.broker.GetLotSize(instrumentUID)
	if err != nil || lotSize <= 0 {
		e.logger.Warn("lot size unavailable, assuming 1", "ticker", ticker, "err", err)
		return 1
	}
	return int64(lotSize)
}

func (e *Executor) executeBuy(d ai.AIDecision) {
	if int(d.Confidence) < e.config.Trading.MinConfidence {
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

	// Scale position size by confidence
	maxPosition := scalePositionByConfidence(e.config.Trading.MaxPositionRub, int(d.Confidence))
	if maxPosition > availableRub {
		maxPosition = availableRub
	}

	// Resolve ticker to instrument UID
	instrumentUID, err := e.broker.ResolveTickerToUID(d.Ticker)
	if err != nil {
		e.logger.Error("resolve ticker", "ticker", d.Ticker, "error", err)
		return
	}

	if !e.apiTradable(d.Ticker, instrumentUID) {
		return
	}

	// Get real last price for lots calculation
	lastPrice := e.broker.GetLastPrice(instrumentUID)
	if lastPrice <= 0 {
		// Fallback to estimate from SL/TP
		lastPrice = d.StopLoss * 1.05
		if lastPrice <= 0 {
			lastPrice = d.TakeProfit * 0.95
		}
	}
	if lastPrice <= 0 {
		e.logger.Error("cannot determine price for lots calculation", "ticker", d.Ticker)
		return
	}

	// Check spread before buying
	spreadPct := e.broker.GetSpreadPct(instrumentUID)
	maxSpread := e.config.Trading.MaxSpreadPct
	if spreadPct > 0 {
		e.logger.Info("BUY spread check", "ticker", d.Ticker,
			"spread", fmt.Sprintf("%.3f%%", spreadPct), "max", fmt.Sprintf("%.3f%%", maxSpread))
	}
	if maxSpread > 0 && spreadPct > maxSpread {
		e.logger.Info("BUY skipped: spread too wide",
			"ticker", d.Ticker, "spread", spreadPct, "max", maxSpread)
		return
	}

	// Лимит позиции задан в рублях, а заявка — в лотах: считаем от цены лота,
	// иначе для бумаг с лотом >1 акции позиция раздувается в lotSize раз.
	pricePerLot := lastPrice * float64(e.lotSizeOrOne(d.Ticker, instrumentUID))
	lots := e.broker.CalculateLots(instrumentUID, pricePerLot, maxPosition)
	if lots < 1 {
		e.logger.Info("BUY skipped: insufficient balance for 1 lot",
			"ticker", d.Ticker, "price_per_lot", pricePerLot, "maxPosition", maxPosition)
		return
	}

	// Execute buy order (limit if configured, otherwise market)
	var result *broker.OrderResult
	slippage := e.config.Trading.LimitOrderSlippage
	if slippage > 0 && lastPrice > 0 {
		limitPrice := lastPrice * (1 + slippage/100)
		result, err = e.broker.BuyWithPrice(instrumentUID, lots, limitPrice)
	} else {
		result, err = e.broker.Buy(instrumentUID, lots)
	}
	if err != nil {
		e.logger.Error("buy order failed", "ticker", d.Ticker, "error", err)
		e.notifier.NotifyError("BUY "+d.Ticker, err)
		return
	}

	if result.ExecutedLots <= 0 || result.ExecutedPrice <= 0 {
		e.logger.Error("BUY order returned zero lots/price, not saving",
			"ticker", d.Ticker, "lots", result.ExecutedLots, "price", result.ExecutedPrice)
		return
	}

	executedPrice := result.ExecutedPrice

	// Calculate SL/TP prices with validation
	slPrice := d.StopLoss
	tpPrice := d.TakeProfit

	cfg := e.config.Trading

	// Dynamic SL floor based on ATR: widen SL on volatile tickers so market noise
	// doesn't knock us out. Fall back to static min_stop_loss_pct otherwise.
	slFloorPct := cfg.MinStopLossPct
	if ind, ok := e.indicators[d.Ticker]; ok && ind.ATR14 > 0 && executedPrice > 0 && cfg.ATRStopLossMultiplier > 0 {
		atrPct := ind.ATR14 / executedPrice * 100
		dynamicFloor := cfg.ATRStopLossMultiplier * atrPct
		if dynamicFloor > slFloorPct {
			slFloorPct = dynamicFloor
		}
	}
	// Cap SL at a sane maximum so a single trade doesn't risk too much capital.
	if slFloorPct > 6.0 {
		slFloorPct = 6.0
	}

	defaultSL := executedPrice * (1 - cfg.DefaultStopLossPct/100)
	defaultTP := executedPrice * (1 + cfg.DefaultTakeProfitPct/100)
	minSL := executedPrice * (1 - slFloorPct/100)
	minTP := executedPrice * (1 + cfg.MinTakeProfitPct/100)

	// Fix inverted or missing SL (SL must be below entry)
	if slPrice <= 0 || slPrice >= executedPrice {
		e.logger.Info("SL corrected: invalid or inverted",
			"ticker", d.Ticker, "original_sl", slPrice, "new_sl", defaultSL)
		slPrice = defaultSL
	}

	// Enforce minimum SL distance
	if slPrice > minSL {
		e.logger.Info("SL corrected: too tight",
			"ticker", d.Ticker, "original_sl", slPrice, "min_sl", minSL)
		slPrice = minSL
	}

	// Fix missing TP
	if tpPrice <= 0 || tpPrice <= executedPrice {
		tpPrice = defaultTP
	}

	// Enforce minimum TP distance
	if tpPrice < minTP {
		e.logger.Info("TP corrected: too tight",
			"ticker", d.Ticker, "original_tp", tpPrice, "min_tp", minTP)
		tpPrice = minTP
	}

	// Enforce minimum risk/reward ratio
	slDistance := executedPrice - slPrice
	if slDistance > 0 && cfg.MinRiskRewardRatio > 0 {
		requiredTP := executedPrice + slDistance*cfg.MinRiskRewardRatio
		if tpPrice < requiredTP {
			e.logger.Info("TP adjusted for R:R ratio",
				"ticker", d.Ticker, "original_tp", tpPrice, "adjusted_tp", requiredTP,
				"rr", cfg.MinRiskRewardRatio)
			tpPrice = requiredTP
		}
	}

	// Final safety: SL must be strictly below entry price
	if slPrice >= executedPrice {
		e.logger.Info("SL final safety: still above entry after corrections, forcing default",
			"ticker", d.Ticker, "sl", slPrice, "entry", executedPrice, "new_sl", defaultSL)
		slPrice = defaultSL
	}

	// Place stop orders
	slOrderID, _ := e.broker.PlaceStopLoss(instrumentUID, result.ExecutedLots, slPrice)
	tpOrderID, _ := e.broker.PlaceTakeProfit(instrumentUID, result.ExecutedLots, tpPrice)

	// Save trade to DB
	entryFeatures := ""
	if e.features != nil {
		entryFeatures = e.features[d.Ticker]
	}
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
		Reasoning:         d.Reasoning,
		EntryFeatures:     entryFeatures,
		Status:            "open",
	}
	if err := e.repo.SaveTrade(trade); err != nil {
		e.logger.Error("save trade", "error", err)
	}

	// Save embedding for vector pattern memory
	if e.vectorStore != nil && trade.EntryFeatures != "" {
		go func(id uint, ticker, feats string) {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			if err := e.vectorStore.SaveEntry(ctx, id, ticker, feats); err != nil {
				e.logger.Error("save pattern embedding", "ticker", ticker, "error", err)
			}
		}(trade.ID, trade.Ticker, trade.EntryFeatures)
	}

	e.notifier.NotifyBuy(d.Ticker, executedPrice, result.ExecutedLots, slPrice, tpPrice, d.Reasoning)
	e.logger.Info("BUY executed",
		"ticker", d.Ticker, "price", executedPrice, "lots", result.ExecutedLots,
		"sl", slPrice, "tp", tpPrice)
}

func (e *Executor) executeSell(d ai.AIDecision) {
	// 1. Sell-cooldown: если по этому тикеру недавно был fail, не дёргаем брокера снова.
	if t, ok := e.failedSellAt[d.Ticker]; ok && time.Since(t) < sellFailureCooldown {
		remaining := sellFailureCooldown - time.Since(t)
		e.logger.Info("SELL skipped: in failure cooldown",
			"ticker", d.Ticker, "remaining_min", int(remaining.Minutes()))
		return
	}

	// Find open trade in DB
	openTrade, err := e.repo.GetOpenTradeByTicker(d.Ticker)
	if err != nil {
		// No DB record — check if broker has this position (orphaned position)
		e.sellOrphanedPosition(d)
		return
	}

	if openTrade.Quantity <= 0 {
		e.logger.Error("SELL skipped: open trade has zero quantity, closing record",
			"ticker", d.Ticker)
		openTrade.Status = "closed"
		openTrade.PnL = 0
		_ = e.repo.UpdateTrade(openTrade)
		// Try to sell orphaned position at broker
		e.sellOrphanedPosition(d)
		return
	}

	instrumentUID, err := e.broker.ResolveTickerToUID(d.Ticker)
	if err != nil {
		e.logger.Error("resolve ticker for sell", "ticker", d.Ticker, "error", err)
		return
	}

	if !e.apiTradable(d.Ticker, instrumentUID) {
		return
	}

	// 2. Sanity-check quantity vs реальная позиция у брокера. Защита от рассинхрона
	// (например, после adopt orphan-позиции или ручной частичной продажи через приложение).
	qty := openTrade.Quantity
	switch broker := e.brokerLotsForTicker(d.Ticker, instrumentUID); {
	case broker == 0:
		e.logger.Info("SELL skipped: position not present at broker, closing DB record",
			"ticker", d.Ticker, "db_lots", qty)
		openTrade.Status = "closed"
		openTrade.PnL = 0
		_ = e.repo.UpdateTrade(openTrade)
		return
	case broker > 0 && broker < qty:
		e.logger.Info("SELL adjusting quantity to broker actual",
			"ticker", d.Ticker, "db_lots", qty, "broker_lots", broker)
		qty = broker
	}

	// Execute sell order (limit if configured, otherwise market)
	var result *broker.OrderResult
	slippage := e.config.Trading.LimitOrderSlippage
	if slippage > 0 {
		lastPrice := e.broker.GetLastPrice(instrumentUID)
		if lastPrice > 0 {
			limitPrice := lastPrice * (1 - slippage/100)
			result, err = e.broker.SellWithPrice(instrumentUID, qty, limitPrice)
		} else {
			result, err = e.broker.Sell(instrumentUID, qty)
		}
	} else {
		result, err = e.broker.Sell(instrumentUID, qty)
	}
	if err != nil {
		e.failedSellAt[d.Ticker] = time.Now()
		e.logger.Error("sell order failed", "ticker", d.Ticker, "lots", qty, "error", err)
		e.notifier.NotifyError("SELL "+d.Ticker, err)
		return
	}

	// 3. price=0 lots=0 — это, как правило, async-исполнение или подвисание ответа,
	// а не реальный фейл. Не паникуем нотификацией — на следующем цикле reconcile
	// обработает позицию (закроет, если её больше нет у брокера).
	if result.ExecutedPrice <= 0 || result.ExecutedLots <= 0 {
		e.logger.Info("SELL pending: broker returned zero price/lots (likely async exec), reconcile will handle",
			"ticker", d.Ticker, "price", result.ExecutedPrice, "lots", result.ExecutedLots)
		return
	}

	// Cancel stop orders
	e.broker.CancelStopOrders(openTrade.StopLossOrderID, openTrade.TakeProfitOrderID)

	// Calculate PnL with commission
	// PnL считаем по фактически исполненному количеству (после sanity-check qty могло
	// быть скорректировано к реальной позиции у брокера).
	executedQty := result.ExecutedLots
	if executedQty <= 0 {
		executedQty = qty
	}
	// Цены — за акцию, quantity — в лотах: переводим в акции, иначе PnL занижается
	// в lotSize раз для бумаг с лотом >1 акции.
	shares := float64(executedQty * e.lotSizeOrOne(d.Ticker, instrumentUID))
	grossPnl := (result.ExecutedPrice - openTrade.Price) * shares
	commissionPct := e.config.Trading.CommissionPct
	commission := (openTrade.Price + result.ExecutedPrice) * shares * commissionPct / 100
	pnl := grossPnl - commission

	// Update trade in DB
	openTrade.PnL = pnl
	openTrade.Status = "closed"
	if err := e.repo.UpdateTrade(openTrade); err != nil {
		e.logger.Error("update trade", "error", err)
	}

	// Record trade outcome for journal
	if e.journal != nil {
		e.journal.RecordOutcome(openTrade, result.ExecutedPrice, pnl, d.Reasoning)
	}

	// Update vector pattern memory with outcome
	if e.vectorStore != nil {
		outcome := "loss"
		if pnl > 0 {
			outcome = "win"
		} else if pnl == 0 {
			outcome = "breakeven"
		}
		holdHours := time.Since(openTrade.CreatedAt).Hours()
		if err := e.vectorStore.UpdateOutcome(openTrade.ID, outcome, pnl, holdHours); err != nil {
			e.logger.Error("update pattern outcome", "ticker", d.Ticker, "error", err)
		}
	}

	// Save sell trade record
	sellTrade := &storage.Trade{
		Ticker:    d.Ticker,
		Action:    "SELL",
		Price:     result.ExecutedPrice,
		Quantity:  result.ExecutedLots,
		OrderID:   result.OrderID,
		PnL:       pnl,
		Reasoning: d.Reasoning,
		Status:    "closed",
	}
	if err := e.repo.SaveTrade(sellTrade); err != nil {
		e.logger.Error("save sell trade", "error", err)
	}

	e.notifier.NotifySell(d.Ticker, result.ExecutedPrice, result.ExecutedLots, pnl, d.Reasoning)
	e.logger.Info("SELL executed",
		"ticker", d.Ticker, "price", result.ExecutedPrice, "lots", result.ExecutedLots, "pnl", pnl)
}

// sellOrphanedPosition sells a position that exists at the broker but not in DB.
func (e *Executor) sellOrphanedPosition(d ai.AIDecision) {
	portfolio, err := e.broker.GetPortfolio()
	if err != nil {
		e.logger.Info("SELL skipped: no open position in DB or broker", "ticker", d.Ticker)
		return
	}

	var qty int64
	for _, pos := range portfolio.Positions {
		if pos.Ticker == d.Ticker && pos.Quantity > 0 {
			qty = int64(pos.Quantity)
			break
		}
	}
	if qty <= 0 {
		e.logger.Info("SELL skipped: no position at broker", "ticker", d.Ticker)
		return
	}

	e.logger.Info("selling orphaned position (exists at broker, not in DB)",
		"ticker", d.Ticker, "quantity", qty)

	instrumentUID, err := e.broker.ResolveTickerToUID(d.Ticker)
	if err != nil {
		e.logger.Error("resolve ticker for orphan sell", "ticker", d.Ticker, "error", err)
		return
	}

	if !e.apiTradable(d.Ticker, instrumentUID) {
		return
	}

	var result *broker.OrderResult
	slippage := e.config.Trading.LimitOrderSlippage
	if slippage > 0 {
		lastPrice := e.broker.GetLastPrice(instrumentUID)
		if lastPrice > 0 {
			limitPrice := lastPrice * (1 - slippage/100)
			result, err = e.broker.SellWithPrice(instrumentUID, qty, limitPrice)
		} else {
			result, err = e.broker.Sell(instrumentUID, qty)
		}
	} else {
		result, err = e.broker.Sell(instrumentUID, qty)
	}
	if err != nil {
		e.logger.Error("orphan sell failed", "ticker", d.Ticker, "error", err)
		return
	}

	if result.ExecutedPrice <= 0 || result.ExecutedLots <= 0 {
		e.logger.Error("orphan sell returned zero price/lots",
			"ticker", d.Ticker, "price", result.ExecutedPrice, "lots", result.ExecutedLots)
		return
	}

	sellTrade := &storage.Trade{
		Ticker:    d.Ticker,
		Action:    "SELL",
		Price:     result.ExecutedPrice,
		Quantity:  result.ExecutedLots,
		OrderID:   result.OrderID,
		PnL:       0,
		Reasoning: "orphaned position cleanup: " + d.Reasoning,
		Status:    "closed",
	}
	if err := e.repo.SaveTrade(sellTrade); err != nil {
		e.logger.Error("save orphan sell trade", "error", err)
	}

	e.notifier.NotifySell(d.Ticker, result.ExecutedPrice, result.ExecutedLots, 0, "orphaned position cleanup")
	e.logger.Info("orphaned position sold",
		"ticker", d.Ticker, "price", result.ExecutedPrice, "lots", result.ExecutedLots)
}

// scalePositionByConfidence scales max position size based on AI confidence level.
func scalePositionByConfidence(maxRub float64, confidence int) float64 {
	switch {
	case confidence >= 90:
		return maxRub // 100%
	case confidence >= 80:
		return maxRub * 0.75 // 75%
	default:
		return maxRub * 0.50 // 50%
	}
}

// brokerLotsForTicker возвращает фактическое количество лотов по тикеру у брокера:
//   - 0   — позиции у брокера нет (надо закрыть запись локально, не дёргать SELL).
//   - >0  — лотов у брокера; вызывающий код использует min(db, broker).
//   - -1  — данных нет (нет снапшота портфеля или не удалось получить lot size).
//     В этом случае sanity-check пропускается, продаём по DB-значению.
func (e *Executor) brokerLotsForTicker(ticker, instrumentUID string) int64 {
	if e.currentPortfolio == nil {
		return -1
	}
	for _, p := range e.currentPortfolio.Positions {
		if p.Ticker != ticker {
			continue
		}
		if p.Quantity <= 0 {
			return 0
		}
		lotSize, err := e.broker.GetLotSize(instrumentUID)
		if err != nil || lotSize <= 0 {
			return -1
		}
		return int64(p.Quantity) / int64(lotSize)
	}
	return 0 // тикер не найден в портфеле = нет позиции
}

// apiTradable проверяет, можно ли торговать инструментом через API. Если нельзя
// (api_trade_available_flag=false → T-Invest отклоняет ордер кодом 30052), ставить
// заявку бесполезно: BUY всё равно не пройдёт, а позицию для SELL придётся продавать
// вручную в приложении. Чтобы не спамить телегу одинаковой ошибкой каждый цикл,
// уведомляем один раз на тикер за жизнь процесса. Возвращает true, если торговать можно.
func (e *Executor) apiTradable(ticker, instrumentUID string) bool {
	if e.broker.IsAPITradeAvailable(instrumentUID) {
		return true
	}

	e.logger.Info("order skipped: instrument forbidden for trading via API (T-Invest 30052)",
		"ticker", ticker)

	if !e.apiForbiddenNotified[ticker] {
		e.apiForbiddenNotified[ticker] = true
		e.notifier.NotifyError(ticker,
			fmt.Errorf("инструмент недоступен для торговли через API (код 30052) — операцию нужно выполнить вручную в приложении"))
	}
	return false
}

func DecisionsToJSON(decisions []ai.AIDecision) string {
	if decisions == nil {
		decisions = []ai.AIDecision{}
	}
	data, err := json.Marshal(decisions)
	if err != nil {
		return "[]"
	}
	return string(data)
}
