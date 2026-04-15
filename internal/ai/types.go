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
	WinRate7d    float64  // win rate over last 7 days (0-100)
	AvgProfit    float64  // average profit on winning trades
	AvgLoss      float64  // average loss on losing trades
	TotalPnL7d   float64  // total P&L over last 7 days
	TradeCount7d int      // number of closed trades in 7 days
	WorstTickers []string // tickers with worst P&L
	LosingStreak int      // consecutive losing trades (most recent)
}

// MarketContext describes broad market regime for the AI prompt.
type MarketContext struct {
	IndexTicker  string  // e.g. "IMOEX"
	ChangePct1d  float64 // % change over 1 day
	ChangePct3d  float64 // % change over 3 days
	ChangePct1w  float64 // % change over 1 week
	Regime       string  // "uptrend", "downtrend", "range", "unknown"
}

type AnalysisRequest struct {
	Tickers          []TickerAnalysis
	GlobalNews       []string
	Positions        []broker.PositionInfo
	RecentTrades     []RecentClosedTrade
	OpenContext      map[string]OpenTradeContext // ticker → контекст открытой позиции
	AvailableRub     float64
	TotalRub         float64
	MaxOpenPositions int // лимит открытых позиций
	Stats            PerformanceStats
	CurrentTime      time.Time     // current time in MSK
	Market           MarketContext // общий фон рынка
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
	Confidence float64 `json:"confidence"` // 0-100 (screening) or 0-1 (position manager)
	Reasoning  string  `json:"reasoning"`
}

// ScreeningRequest is input for the Screening Agent (BUY candidates only).
type ScreeningRequest struct {
	TickerFeatures  []string            // textual features per ticker
	Market          MarketContext
	GlobalNews      []string
	TickerNews      map[string][]string
	Lessons         []string            // formatted lesson lines
	TodayTraded     []string
	Stats           PerformanceStats
	CurrentTime     time.Time
	AvailableRub    float64
	SimilarPatterns map[string][]PatternMatchInfo // ticker → similar historical patterns
}

// PositionContext is a single open position for Position Manager.
type PositionContext struct {
	Ticker       string
	EntryPrice   float64
	CurrentPrice float64
	PnLPct       float64
	Quantity     int64
	HoldDuration string
	StopLoss     float64
	TakeProfit   float64
	ProgressToTP float64
	Hypothesis   string
	Features     string   // current textual features
	News         []string
}

// PositionRequest is input for the Position Manager Agent.
type PositionRequest struct {
	Positions   []PositionContext
	Lessons     []string
	CurrentTime time.Time
	Market      MarketContext
}

// JournalReviewRequest is input for the daily Trade Journal review.
type JournalReviewRequest struct {
	Outcomes        []JournalOutcome
	Stats           JournalReviewStats
	PreviousLessons []string
	CurrentDate     time.Time
}

type JournalOutcome struct {
	Ticker        string
	Hypothesis    string
	EntryFeatures string
	ExitReason    string
	Outcome       string // "win" | "loss" | "breakeven"
	PnL           float64
	HoldHours     float64
	WhatHappened  string
}

type JournalReviewStats struct {
	TotalTrades      int
	WinRate          float64
	AvgWinPnL        float64
	AvgLossPnL       float64
	AvgHoldHoursWin  float64
	AvgHoldHoursLoss float64
	ByExitReason     map[string]int
}

// PatternMatchInfo is a simplified pattern match for the screening prompt.
type PatternMatchInfo struct {
	Features   string
	Outcome    string
	PnL        float64
	HoldHours  float64
	Similarity float64
}
