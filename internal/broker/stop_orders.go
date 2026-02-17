package broker

import (
	"fmt"

	"github.com/russianinvestments/invest-api-go-sdk/investgo"
	pb "github.com/russianinvestments/invest-api-go-sdk/proto"
)

func (bc *BrokerClient) PlaceStopLoss(instrumentID string, lots int64, stopPrice float64) (string, error) {
	if bc.Config.IsSandbox() {
		// Stop orders are not supported in sandbox
		bc.Logger.Info("stop-loss skipped in sandbox mode", "instrument", instrumentID, "price", stopPrice)
		return "", nil
	}

	stopOrders := bc.Client.NewStopOrdersServiceClient()
	resp, err := stopOrders.PostStopOrder(&investgo.PostStopOrderRequest{
		InstrumentId:  instrumentID,
		Quantity:      lots,
		StopPrice:     floatToSimpleQuotation(stopPrice),
		Direction:     pb.StopOrderDirection_STOP_ORDER_DIRECTION_SELL,
		AccountId:     bc.AccountID(),
		ExpirationType: pb.StopOrderExpirationType_STOP_ORDER_EXPIRATION_TYPE_GOOD_TILL_CANCEL,
		StopOrderType: pb.StopOrderType_STOP_ORDER_TYPE_STOP_LOSS,
		OrderID:       investgo.CreateUid(),
	})
	if err != nil {
		return "", fmt.Errorf("place stop loss: %w", err)
	}

	return resp.GetStopOrderId(), nil
}

func (bc *BrokerClient) PlaceTakeProfit(instrumentID string, lots int64, targetPrice float64) (string, error) {
	if bc.Config.IsSandbox() {
		bc.Logger.Info("take-profit skipped in sandbox mode", "instrument", instrumentID, "price", targetPrice)
		return "", nil
	}

	stopOrders := bc.Client.NewStopOrdersServiceClient()
	resp, err := stopOrders.PostStopOrder(&investgo.PostStopOrderRequest{
		InstrumentId:  instrumentID,
		Quantity:      lots,
		StopPrice:     floatToSimpleQuotation(targetPrice),
		Direction:     pb.StopOrderDirection_STOP_ORDER_DIRECTION_SELL,
		AccountId:     bc.AccountID(),
		ExpirationType: pb.StopOrderExpirationType_STOP_ORDER_EXPIRATION_TYPE_GOOD_TILL_CANCEL,
		StopOrderType: pb.StopOrderType_STOP_ORDER_TYPE_TAKE_PROFIT,
		OrderID:       investgo.CreateUid(),
	})
	if err != nil {
		return "", fmt.Errorf("place take profit: %w", err)
	}

	return resp.GetStopOrderId(), nil
}

func (bc *BrokerClient) CancelStopOrders(slOrderID, tpOrderID string) {
	if bc.Config.IsSandbox() {
		return
	}

	stopOrders := bc.Client.NewStopOrdersServiceClient()

	if slOrderID != "" {
		if _, err := stopOrders.CancelStopOrder(bc.AccountID(), slOrderID); err != nil {
			bc.Logger.Error("cancel stop loss", "order_id", slOrderID, "error", err)
		}
	}
	if tpOrderID != "" {
		if _, err := stopOrders.CancelStopOrder(bc.AccountID(), tpOrderID); err != nil {
			bc.Logger.Error("cancel take profit", "order_id", tpOrderID, "error", err)
		}
	}
}

func floatToSimpleQuotation(value float64) *pb.Quotation {
	units := int64(value)
	nano := int32((value - float64(units)) * 1e9)
	return &pb.Quotation{Units: units, Nano: nano}
}
