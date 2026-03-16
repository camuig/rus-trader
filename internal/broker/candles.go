package broker

import (
	"sync"
	"time"

	pb "github.com/russianinvestments/invest-api-go-sdk/proto"

	"github.com/camuig/rus-trader/internal/indicators"
)

type PeriodOHLCV struct {
	Open   float64
	High   float64
	Low    float64
	Close  float64
	Volume float64
}

type CandleSnapshot struct {
	Ticker        string
	InstrumentUID string
	LastPrice     float64
	Period3h      PeriodOHLCV
	Period1d      PeriodOHLCV
	Period3d      PeriodOHLCV
	Period1w      PeriodOHLCV
	Indicators    indicators.Indicators
	HourlyCandles []indicators.Candle // raw hourly candles for screening
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

	// Convert to indicator candles for technical analysis
	hourly := make([]indicators.Candle, 0, len(candles))
	for _, c := range candles {
		hourly = append(hourly, indicators.Candle{
			Open:   c.GetOpen().ToFloat(),
			High:   c.GetHigh().ToFloat(),
			Low:    c.GetLow().ToFloat(),
			Close:  c.GetClose().ToFloat(),
			Volume: float64(c.GetVolume()),
		})
	}

	snap := &CandleSnapshot{
		Ticker:        ticker,
		InstrumentUID: uid,
		LastPrice:     findCloseAtOffset(candles, now, 0),
		Period3h:      aggregateOHLCV(candles, now, 3*time.Hour),
		Period1d:      aggregateOHLCV(candles, now, 24*time.Hour),
		Period3d:      aggregateOHLCV(candles, now, 3*24*time.Hour),
		Period1w:      aggregateOHLCV(candles, now, 7*24*time.Hour),
		Indicators:    indicators.Compute(hourly),
		HourlyCandles: hourly,
	}

	return snap, nil
}

// aggregateOHLCV aggregates hourly candles for the given period into OHLCV.
// Open = first candle's open, High = max high, Low = min low, Close = last candle's close, Volume = sum.
func aggregateOHLCV(candles []*pb.HistoricCandle, now time.Time, period time.Duration) PeriodOHLCV {
	cutoff := now.Add(-period)

	var filtered []*pb.HistoricCandle
	for _, c := range candles {
		if c.GetTime().AsTime().After(cutoff) || c.GetTime().AsTime().Equal(cutoff) {
			filtered = append(filtered, c)
		}
	}

	if len(filtered) == 0 {
		return PeriodOHLCV{}
	}

	result := PeriodOHLCV{
		Open:  filtered[0].GetOpen().ToFloat(),
		High:  filtered[0].GetHigh().ToFloat(),
		Low:   filtered[0].GetLow().ToFloat(),
		Close: filtered[len(filtered)-1].GetClose().ToFloat(),
	}

	for _, c := range filtered {
		h := c.GetHigh().ToFloat()
		l := c.GetLow().ToFloat()
		if h > result.High {
			result.High = h
		}
		if l < result.Low {
			result.Low = l
		}
		result.Volume += float64(c.GetVolume())
	}

	return result
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
