package broker

import (
	"sync"
	"time"

	pb "github.com/russianinvestments/invest-api-go-sdk/proto"
)

type CandleSnapshot struct {
	Ticker        string
	InstrumentUID string
	LastPrice     float64
	Price3hAgo    float64
	Price1dAgo    float64
	Price3dAgo    float64
	Price1wAgo    float64
	Volume24h     float64
}

func (bc *BrokerClient) FetchCandleSnapshots(tickers []string, concurrency int) []CandleSnapshot {
	if concurrency <= 0 {
		concurrency = 10
	}

	var (
		mu      sync.Mutex
		wg      sync.WaitGroup
		sem     = make(chan struct{}, concurrency)
		results []CandleSnapshot
	)

	for _, ticker := range tickers {
		wg.Add(1)
		sem <- struct{}{}

		go func(t string) {
			defer wg.Done()
			defer func() { <-sem }()

			snap, err := bc.fetchOneTicker(t)
			if err != nil {
				bc.Logger.Error("fetch candles", "ticker", t, "error", err)
				return
			}

			mu.Lock()
			results = append(results, *snap)
			mu.Unlock()
		}(ticker)
	}

	wg.Wait()
	return results
}

func (bc *BrokerClient) fetchOneTicker(ticker string) (*CandleSnapshot, error) {
	uid, err := bc.ResolveTickerToUID(ticker)
	if err != nil {
		return nil, err
	}

	now := time.Now()
	from := now.Add(-7 * 24 * time.Hour)

	md := bc.Client.NewMarketDataServiceClient()
	resp, err := md.GetCandles(
		uid,
		pb.CandleInterval_CANDLE_INTERVAL_HOUR,
		from, now,
		pb.GetCandlesRequest_CANDLE_SOURCE_EXCHANGE,
		0,
	)
	if err != nil {
		return nil, err
	}

	candles := resp.GetCandles()
	if len(candles) == 0 {
		return nil, nil
	}

	snap := &CandleSnapshot{
		Ticker:        ticker,
		InstrumentUID: uid,
		LastPrice:     findCloseAtOffset(candles, now, 0),
		Price3hAgo:    findCloseAtOffset(candles, now, 3*time.Hour),
		Price1dAgo:    findCloseAtOffset(candles, now, 24*time.Hour),
		Price3dAgo:    findCloseAtOffset(candles, now, 3*24*time.Hour),
		Price1wAgo:    findCloseAtOffset(candles, now, 7*24*time.Hour),
		Volume24h:     sumVolume24h(candles, now),
	}

	return snap, nil
}

// findCloseAtOffset finds the close price of the candle closest to (now - offset).
func findCloseAtOffset(candles []*pb.HistoricCandle, now time.Time, offset time.Duration) float64 {
	target := now.Add(-offset)
	var bestCandle *pb.HistoricCandle
	var bestDiff time.Duration

	for _, c := range candles {
		t := c.GetTime().AsTime()
		diff := absDuration(t.Sub(target))
		if bestCandle == nil || diff < bestDiff {
			bestCandle = c
			bestDiff = diff
		}
	}

	if bestCandle == nil {
		return 0
	}
	return bestCandle.GetClose().ToFloat()
}

func sumVolume24h(candles []*pb.HistoricCandle, now time.Time) float64 {
	cutoff := now.Add(-24 * time.Hour)
	var total float64
	for _, c := range candles {
		if c.GetTime().AsTime().After(cutoff) {
			total += float64(c.GetVolume())
		}
	}
	return total
}

func absDuration(d time.Duration) time.Duration {
	if d < 0 {
		return -d
	}
	return d
}

// FilterTradable checks which instrument UIDs are available for API trading.
func (bc *BrokerClient) FilterTradable(uids []string) (map[string]bool, error) {
	if len(uids) == 0 {
		return map[string]bool{}, nil
	}

	md := bc.Client.NewMarketDataServiceClient()
	resp, err := md.GetTradingStatuses(uids)
	if err != nil {
		return nil, err
	}

	result := make(map[string]bool, len(uids))
	for _, s := range resp.GetTradingStatuses() {
		result[s.GetInstrumentUid()] = s.GetApiTradeAvailableFlag() && s.GetMarketOrderAvailableFlag()
	}
	return result, nil
}
