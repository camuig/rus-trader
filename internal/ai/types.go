package ai

import (
	"time"

	"github.com/camuig/rus-trader/internal/broker"
)

type PeriodData struct {
	Open      float64
	High      float64
	Low       float64
	Close     float64
	Volume    float64
	ChangePct float64
}

type TickerAnalysis struct {
	Ticker    string
	LastPrice float64
	Period3h  PeriodData
	Period1d  PeriodData
	Period3d  PeriodData
	Period1w  PeriodData
	News      []string // заголовки новостей
}

type RecentClosedTrade struct {
	Ticker     string
	EntryPrice float64
	ExitPrice  float64
	Quantity   int64
	PnL        float64
	ClosedAt   time.Time
	Reasoning  string // причина закрытия
}

type OpenTradeContext struct {
	Reasoning       string
	OpenedAt        time.Time
	StopLossPrice   float64
	TakeProfitPrice float64
}

type AnalysisRequest struct {
	Tickers      []TickerAnalysis
	Positions    []broker.PositionInfo
	RecentTrades []RecentClosedTrade
	OpenContext  map[string]OpenTradeContext // ticker → контекст открытой позиции
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
