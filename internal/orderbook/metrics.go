package orderbook

import (
	"github.com/camuig/rus-trader/internal/broker"
)

type OrderBookMetrics struct {
	Ticker          string
	BidAskImbalance float64
	SpreadPct       float64
	BidWall         *Wall
	AskWall         *Wall
}

type Wall struct {
	Price  float64
	Volume int64
	Ratio  float64
}

func ComputeMetrics(ticker string, book *broker.OrderBookRaw, wallThreshold float64) OrderBookMetrics {
	m := OrderBookMetrics{Ticker: ticker}
	if book == nil {
		return m
	}

	if book.BestBid > 0 && book.BestAsk > 0 {
		m.SpreadPct = (book.BestAsk - book.BestBid) / book.BestBid * 100
	}

	var bidTotal, askTotal int64
	for _, b := range book.Bids {
		bidTotal += b.Volume
	}
	for _, a := range book.Asks {
		askTotal += a.Volume
	}
	if askTotal > 0 {
		m.BidAskImbalance = float64(bidTotal) / float64(askTotal)
	} else if bidTotal > 0 {
		m.BidAskImbalance = 10.0
	}

	m.BidWall = detectWall(book.Bids, wallThreshold)
	m.AskWall = detectWall(book.Asks, wallThreshold)

	return m
}

func detectWall(levels []broker.OrderBookLevel, threshold float64) *Wall {
	if len(levels) == 0 || threshold <= 0 {
		return nil
	}
	var totalVol int64
	for _, l := range levels {
		totalVol += l.Volume
	}
	avg := float64(totalVol) / float64(len(levels))
	if avg <= 0 {
		return nil
	}

	var best *Wall
	for _, l := range levels {
		ratio := float64(l.Volume) / avg
		if ratio >= threshold {
			if best == nil || l.Volume > best.Volume {
				best = &Wall{Price: l.Price, Volume: l.Volume, Ratio: ratio}
			}
		}
	}
	return best
}
