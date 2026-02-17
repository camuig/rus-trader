package ai

import (
	"time"

	"github.com/camuig/rus-trader/internal/broker"
)

type TickerAnalysis struct {
	Ticker     string
	LastPrice  float64
	Price3hAgo float64
	Price1dAgo float64
	Price3dAgo float64
	Price1wAgo float64
	Volume24h  float64
	Change3h   float64 // процент
	Change1d   float64
	Change3d   float64
	Change1w   float64
	News       []string // заголовки новостей
}

type RecentClosedTrade struct {
	Ticker     string
	EntryPrice float64
	ExitPrice  float64
	Quantity   int64
	PnL        float64
	ClosedAt   time.Time
}

type AnalysisRequest struct {
	Tickers      []TickerAnalysis
	Positions    []broker.PositionInfo
	RecentTrades []RecentClosedTrade
	AvailableRub float64
	TotalRub     float64
}

type AIDecision struct {
	Action     string  `json:"action"`     // BUY, SELL, HOLD
	Ticker     string  `json:"ticker"`
	StopLoss   float64 `json:"stop_loss"`
	TakeProfit float64 `json:"take_profit"`
	Confidence int     `json:"confidence"` // 0-100
	Reasoning  string  `json:"reasoning"`
}
