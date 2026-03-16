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

// GetLastPrice fetches the last trade price for an instrument via market data.
func (bc *BrokerClient) GetLastPrice(instrumentUID string) float64 {
	md := bc.Client.NewMarketDataServiceClient()
	resp, err := md.GetLastPrices([]string{instrumentUID})
	if err != nil {
		bc.Logger.Error("get last price", "instrument", instrumentUID, "error", err)
		return 0
	}
	for _, lp := range resp.GetLastPrices() {
		if lp.GetInstrumentUid() == instrumentUID {
			if p := lp.GetPrice(); p != nil {
				return p.ToFloat()
			}
		}
	}
	return 0
}

// GetSpreadPct fetches the orderbook and calculates bid/ask spread as percentage.
// Returns 0 if orderbook is unavailable (e.g., sandbox mode).
func (bc *BrokerClient) GetSpreadPct(instrumentUID string) float64 {
	md := bc.Client.NewMarketDataServiceClient()
	resp, err := md.GetOrderBook(instrumentUID, 1) // depth=1, only best bid/ask
	if err != nil {
		bc.Logger.Debug("get orderbook", "instrument", instrumentUID, "error", err)
		return 0
	}

	asks := resp.GetAsks()
	bids := resp.GetBids()
	if len(asks) == 0 || len(bids) == 0 {
		return 0
	}

	bestAsk := asks[0].GetPrice().ToFloat()
	bestBid := bids[0].GetPrice().ToFloat()
	if bestBid <= 0 {
		return 0
	}

	return (bestAsk - bestBid) / bestBid * 100
}
