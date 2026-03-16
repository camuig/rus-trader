package broker

import (
	"fmt"
	"math"

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

	return extractOrderResult(resp), nil
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

	return extractOrderResult(resp), nil
}

func extractOrderResult(resp *investgo.PostOrderResponse) *OrderResult {
	result := &OrderResult{
		OrderID:      resp.GetOrderId(),
		ExecutedLots: resp.GetLotsExecuted(),
	}
	if ep := resp.GetExecutedOrderPrice(); ep != nil {
		result.ExecutedPrice = ep.ToFloat()
	}
	return result
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
