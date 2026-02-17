package broker

import (
	"fmt"

	pb "github.com/russianinvestments/invest-api-go-sdk/proto"
)

type PortfolioInfo struct {
	TotalRub     float64
	AvailableRub float64
	Positions    []PositionInfo
}

type PositionInfo struct {
	Ticker        string
	InstrumentUID string
	Figi          string
	Quantity      float64
	AvgPrice      float64
	CurrentPrice  float64
	PnL           float64
}

func (bc *BrokerClient) GetPortfolio() (*PortfolioInfo, error) {
	accountID := bc.AccountID()
	currency := pb.PortfolioRequest_RUB

	var resp interface {
		GetTotalAmountPortfolio() *pb.MoneyValue
		GetTotalAmountCurrencies() *pb.MoneyValue
		GetPositions() []*pb.PortfolioPosition
	}

	if bc.Config.IsSandbox() {
		sandbox := bc.Client.NewSandboxServiceClient()
		r, err := sandbox.GetSandboxPortfolio(accountID, currency)
		if err != nil {
			return nil, fmt.Errorf("get sandbox portfolio: %w", err)
		}
		resp = r.PortfolioResponse
	} else {
		ops := bc.Client.NewOperationsServiceClient()
		r, err := ops.GetPortfolio(accountID, currency)
		if err != nil {
			return nil, fmt.Errorf("get portfolio: %w", err)
		}
		resp = r.PortfolioResponse
	}

	info := &PortfolioInfo{}
	if total := resp.GetTotalAmountPortfolio(); total != nil {
		info.TotalRub = total.ToFloat()
	}
	if currencies := resp.GetTotalAmountCurrencies(); currencies != nil {
		info.AvailableRub = currencies.ToFloat()
	}

	for _, pos := range resp.GetPositions() {
		if pos.GetInstrumentType() == "currency" {
			continue
		}
		pi := PositionInfo{
			InstrumentUID: pos.GetInstrumentUid(),
			Figi:          pos.GetFigi(),
		}
		if ticker, err := bc.resolveInstrumentUID(pi.InstrumentUID); err == nil {
			pi.Ticker = ticker
		}
		if q := pos.GetQuantity(); q != nil {
			pi.Quantity = q.ToFloat()
		}
		if ap := pos.GetAveragePositionPrice(); ap != nil {
			pi.AvgPrice = ap.ToFloat()
		}
		if cp := pos.GetCurrentPrice(); cp != nil {
			pi.CurrentPrice = cp.ToFloat()
		}
		if ey := pos.GetExpectedYield(); ey != nil {
			pi.PnL = ey.ToFloat()
		}
		info.Positions = append(info.Positions, pi)
	}

	return info, nil
}

func (bc *BrokerClient) GetAvailableRub() (float64, error) {
	portfolio, err := bc.GetPortfolio()
	if err != nil {
		return 0, err
	}
	return portfolio.AvailableRub, nil
}
