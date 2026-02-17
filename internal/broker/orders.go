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

func (bc *BrokerClient) Buy(instrumentID string, lots int64) (*OrderResult, error) {
	orderID := investgo.CreateUid()

	req := &investgo.PostOrderRequestShort{
		InstrumentId: instrumentID,
		Quantity:     lots,
		AccountId:    bc.AccountID(),
		OrderType:    pb.OrderType_ORDER_TYPE_MARKET,
		OrderId:      orderID,
	}

	var resp *investgo.PostOrderResponse
	var err error

	if bc.Config.IsSandbox() {
		sandbox := bc.Client.NewSandboxServiceClient()
		resp, err = sandbox.PostSandboxOrder(&investgo.PostOrderRequest{
			InstrumentId: req.InstrumentId,
			Quantity:     req.Quantity,
			Direction:    pb.OrderDirection_ORDER_DIRECTION_BUY,
			AccountId:    req.AccountId,
			OrderType:    req.OrderType,
			OrderId:      req.OrderId,
		})
	} else {
		orders := bc.Client.NewOrdersServiceClient()
		resp, err = orders.Buy(req)
	}

	if err != nil {
		return nil, fmt.Errorf("buy order: %w", err)
	}

	result := &OrderResult{
		OrderID:      resp.GetOrderId(),
		ExecutedLots: resp.GetLotsExecuted(),
	}
	if ep := resp.GetExecutedOrderPrice(); ep != nil {
		result.ExecutedPrice = ep.ToFloat()
	}

	return result, nil
}

func (bc *BrokerClient) Sell(instrumentID string, lots int64) (*OrderResult, error) {
	orderID := investgo.CreateUid()

	req := &investgo.PostOrderRequestShort{
		InstrumentId: instrumentID,
		Quantity:     lots,
		AccountId:    bc.AccountID(),
		OrderType:    pb.OrderType_ORDER_TYPE_MARKET,
		OrderId:      orderID,
	}

	var resp *investgo.PostOrderResponse
	var err error

	if bc.Config.IsSandbox() {
		sandbox := bc.Client.NewSandboxServiceClient()
		resp, err = sandbox.PostSandboxOrder(&investgo.PostOrderRequest{
			InstrumentId: req.InstrumentId,
			Quantity:     req.Quantity,
			Direction:    pb.OrderDirection_ORDER_DIRECTION_SELL,
			AccountId:    req.AccountId,
			OrderType:    req.OrderType,
			OrderId:      req.OrderId,
		})
	} else {
		orders := bc.Client.NewOrdersServiceClient()
		resp, err = orders.Sell(req)
	}

	if err != nil {
		return nil, fmt.Errorf("sell order: %w", err)
	}

	result := &OrderResult{
		OrderID:      resp.GetOrderId(),
		ExecutedLots: resp.GetLotsExecuted(),
	}
	if ep := resp.GetExecutedOrderPrice(); ep != nil {
		result.ExecutedPrice = ep.ToFloat()
	}

	return result, nil
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
