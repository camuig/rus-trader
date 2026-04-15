package orderbook

import (
	"testing"

	"github.com/camuig/rus-trader/internal/broker"
)

func TestComputeMetrics_BuyPressure(t *testing.T) {
	book := &broker.OrderBookRaw{
		Bids: []broker.OrderBookLevel{
			{Price: 100, Volume: 500},
			{Price: 99, Volume: 300},
		},
		Asks: []broker.OrderBookLevel{
			{Price: 101, Volume: 200},
			{Price: 102, Volume: 100},
		},
		BestBid: 100, BestAsk: 101,
	}
	m := ComputeMetrics("TEST", book, 5.0)
	// bid total = 800, ask total = 300, imbalance = 2.67
	if m.BidAskImbalance < 2.0 {
		t.Errorf("expected imbalance > 2.0, got %.2f", m.BidAskImbalance)
	}
	if m.SpreadPct < 0.9 || m.SpreadPct > 1.1 {
		t.Errorf("expected spread ~1%%, got %.2f%%", m.SpreadPct)
	}
}

func TestComputeMetrics_WallDetection(t *testing.T) {
	book := &broker.OrderBookRaw{
		Bids: []broker.OrderBookLevel{
			{Price: 100, Volume: 100},
			{Price: 99, Volume: 100},
			{Price: 98, Volume: 100},
			{Price: 97, Volume: 800}, // wall: 800 vs avg 275
		},
		Asks: []broker.OrderBookLevel{
			{Price: 101, Volume: 50},
		},
		BestBid: 100, BestAsk: 101,
	}
	m := ComputeMetrics("TEST", book, 2.0)
	if m.BidWall == nil {
		t.Fatal("expected bid wall")
	}
	if m.BidWall.Price != 97 {
		t.Errorf("expected wall at 97, got %.2f", m.BidWall.Price)
	}
}

func TestComputeMetrics_EmptyBook(t *testing.T) {
	book := &broker.OrderBookRaw{}
	m := ComputeMetrics("TEST", book, 5.0)
	if m.BidAskImbalance != 0 {
		t.Errorf("expected 0 imbalance, got %.2f", m.BidAskImbalance)
	}
}

func TestComputeMetrics_NilBook(t *testing.T) {
	m := ComputeMetrics("TEST", nil, 5.0)
	if m.BidAskImbalance != 0 {
		t.Errorf("expected 0 for nil book")
	}
}
