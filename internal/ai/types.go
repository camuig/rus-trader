package ai

import (
	"time"

	"github.com/camuig/rus-trader/internal/broker"
	"github.com/camuig/rus-trader/internal/indicators"
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
	Ticker     string
	Brief      string // краткая карточка тикера (имя/тип/лот/валюта/страна)
	LastPrice  float64
	Period3h   PeriodData
	Period1d   PeriodData
	Period3d   PeriodData
	Period1w   PeriodData
	News       []string // заголовки новостей
	Indicators indicators.Indicators
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

// PerformanceStats holds aggregated trading performance for AI context.
type PerformanceStats struct {
	WinRate7d    float64 // win rate over last 7 days (0-100)
	AvgProfit    float64 // average profit on winning trades
	AvgLoss      float64 // average loss on losing trades
	TotalPnL7d   float64 // total P&L over last 7 days
	TradeCount7d int     // number of closed trades in 7 days
	WorstTickers []string // tickers with worst P&L
}

type AnalysisRequest struct {
	Tickers      []TickerAnalysis
	GlobalNews   []string
	Positions    []broker.PositionInfo
	RecentTrades []RecentClosedTrade
	OpenContext  map[string]OpenTradeContext // ticker → контекст открытой позиции
	AvailableRub float64
	TotalRub     float64
	Stats        PerformanceStats
	CurrentTime  time.Time // current time in MSK
}

type PromptLimits struct {
	MaxChars            int
	MaxTickerBriefChars int
	MaxTickerNewsItems  int
	MaxWorldNewsItems   int
	MaxNewsTitleChars   int
}

type AIDecision struct {
	Action     string  `json:"action"` // BUY, SELL, HOLD
	Ticker     string  `json:"ticker"`
	StopLoss   float64 `json:"stop_loss"`
	TakeProfit float64 `json:"take_profit"`
	Confidence int     `json:"confidence"` // 0-100
	Reasoning  string  `json:"reasoning"`
}
