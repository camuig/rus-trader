package broker

import (
	"fmt"
	"math"

	"github.com/camuig/rus-trader/internal/logger"
	"github.com/russianinvestments/invest-api-go-sdk/investgo"
	pb "github.com/russianinvestments/invest-api-go-sdk/proto"
)

type OrderResult struct {
	OrderID       string
	ExecutedPrice float64
	ExecutedLots  int64
}

// Buy places a buy order. If limitPrice > 0, uses a limit order; otherwise market.
func (bc *BrokerClient) Buy(instrumentID string, lots int64) (*OrderResult, error) {
	return bc.BuyWithPrice(instrumentID, lots, 0)
}

// BuyWithPrice places a buy order with optional limit price.
func (bc *BrokerClient) BuyWithPrice(instrumentID string, lots int64, limitPrice float64) (*OrderResult, error) {
	orderID := investgo.CreateUid()

	orderType := pb.OrderType_ORDER_TYPE_MARKET
	if limitPrice > 0 {
		orderType = pb.OrderType_ORDER_TYPE_LIMIT
	}

	orderReq := &investgo.PostOrderRequest{
		InstrumentId: instrumentID,
		Quantity:     lots,
		Direction:    pb.OrderDirection_ORDER_DIRECTION_BUY,
		AccountId:    bc.AccountID(),
		OrderType:    orderType,
		OrderId:      orderID,
	}
	if limitPrice > 0 {
		orderReq.Price = floatToSimpleQuotation(limitPrice)
	}

	var resp *investgo.PostOrderResponse
	var err error

	if bc.Config.IsSandbox() {
		sandbox := bc.Client.NewSandboxServiceClient()
		resp, err = sandbox.PostSandboxOrder(orderReq)
	} else {
		orders := bc.Client.NewOrdersServiceClient()
		resp, err = orders.PostOrder(orderReq)
	}

	if err != nil {
		return nil, fmt.Errorf("buy order: %w", err)
	}

	return bc.orderResultWithSanityCheck(resp, instrumentID), nil
}

// Sell places a sell order. If limitPrice > 0, uses a limit order; otherwise market.
func (bc *BrokerClient) Sell(instrumentID string, lots int64) (*OrderResult, error) {
	return bc.SellWithPrice(instrumentID, lots, 0)
}

// SellWithPrice places a sell order with optional limit price.
func (bc *BrokerClient) SellWithPrice(instrumentID string, lots int64, limitPrice float64) (*OrderResult, error) {
	orderID := investgo.CreateUid()

	orderType := pb.OrderType_ORDER_TYPE_MARKET
	if limitPrice > 0 {
		orderType = pb.OrderType_ORDER_TYPE_LIMIT
	}

	orderReq := &investgo.PostOrderRequest{
		InstrumentId: instrumentID,
		Quantity:     lots,
		Direction:    pb.OrderDirection_ORDER_DIRECTION_SELL,
		AccountId:    bc.AccountID(),
		OrderType:    orderType,
		OrderId:      orderID,
	}
	if limitPrice > 0 {
		orderReq.Price = floatToSimpleQuotation(limitPrice)
	}

	var resp *investgo.PostOrderResponse
	var err error

	if bc.Config.IsSandbox() {
		sandbox := bc.Client.NewSandboxServiceClient()
		resp, err = sandbox.PostSandboxOrder(orderReq)
	} else {
		orders := bc.Client.NewOrdersServiceClient()
		resp, err = orders.PostOrder(orderReq)
	}

	if err != nil {
		return nil, fmt.Errorf("sell order: %w", err)
	}

	return bc.orderResultWithSanityCheck(resp, instrumentID), nil
}

// execPriceTolerancePct — максимально допустимое отклонение расчётной цены исполнения
// от последней котировки; при большем отклонении цена считается аномальной.
const execPriceTolerancePct = 20.0

func (bc *BrokerClient) orderResultWithSanityCheck(resp *investgo.PostOrderResponse, instrumentID string) *OrderResult {
	lotSize, err := bc.GetLotSize(instrumentID)
	if err != nil {
		bc.Logger.Warn("lot size unavailable, executed price may need quote fallback",
			"instrument", instrumentID, "err", err)
	}
	return extractOrderResult(resp, lotSize, bc.GetLastPrice(instrumentID), bc.Logger)
}

// extractOrderResult converts a PostOrderResponse to OrderResult. ExecutedOrderPrice от
// T-Invest API содержит произведение средней цены исполнения на количество исполненных
// акций (а не цену за акцию), поэтому делим на (ExecutedLots * lotSize). Песочница,
// однако, иногда возвращает уже цену за акцию, а lotSize может быть недоступен, поэтому
// результат сверяется с последней котировкой refPrice: выбирается интерпретация в пределах
// execPriceTolerancePct от котировки, иначе используется сама котировка.
func extractOrderResult(resp *investgo.PostOrderResponse, lotSize int32, refPrice float64, log *logger.Logger) *OrderResult {
	result := &OrderResult{
		OrderID:      resp.GetOrderId(),
		ExecutedLots: resp.GetLotsExecuted(),
	}
	ep := resp.GetExecutedOrderPrice()
	if ep == nil {
		return result
	}

	total := ep.ToFloat()
	perShare := total
	if shares := result.ExecutedLots * int64(lotSize); shares > 0 {
		perShare = total / float64(shares)
	}
	result.ExecutedPrice = perShare

	if refPrice <= 0 || withinPct(perShare, refPrice, execPriceTolerancePct) {
		return result
	}
	if withinPct(total, refPrice, execPriceTolerancePct) {
		result.ExecutedPrice = total
		return result
	}
	if log != nil {
		log.Warn("executed price anomaly, falling back to last quote",
			"order_id", result.OrderID, "raw_total", total, "per_share", perShare, "last_quote", refPrice)
	}
	result.ExecutedPrice = refPrice
	return result
}

// withinPct reports whether a is within pct percent of reference b.
func withinPct(a, b, pct float64) bool {
	if b <= 0 {
		return false
	}
	return math.Abs(a-b)/b*100 <= pct
}

// CalculateLots calculates the number of lots that can be bought for the given amount in RUB.
func (bc *BrokerClient) CalculateLots(instrumentID string, pricePerLot float64, maxRub float64) int64 {
	if pricePerLot <= 0 {
		return 0
	}
	lots := int64(math.Floor(maxRub / pricePerLot))
	if lots < 1 {
		return 0
	}
	return lots
}
