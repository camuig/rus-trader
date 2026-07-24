package storage

import "time"

type Trade struct {
	ID        uint      `gorm:"primarykey" json:"id"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`

	Ticker   string  `gorm:"index;not null" json:"ticker"`
	Action   string  `gorm:"not null" json:"action"` // BUY or SELL
	Price    float64 `gorm:"not null" json:"price"`
	Quantity int64   `gorm:"not null" json:"quantity"`
	OrderID  string  `json:"order_id"`

	StopLossPrice     float64 `json:"stop_loss_price"`
	TakeProfitPrice   float64 `json:"take_profit_price"`
	StopLossOrderID   string  `json:"stop_loss_order_id"`
	TakeProfitOrderID string  `json:"take_profit_order_id"`

	PnL           float64 `gorm:"column:pnl" json:"pnl"`
	Reasoning     string  `gorm:"type:text" json:"reasoning"`
	EntryFeatures string  `gorm:"type:text" json:"entry_features"`
	Status        string  `gorm:"not null;default:'open'" json:"status"` // open, closed
}

type AnalysisLog struct {
	ID        uint      `gorm:"primarykey" json:"id"`
	CreatedAt time.Time `json:"created_at"`

	SignalsCount  int    `json:"signals_count"`
	AIResponse    string `gorm:"type:text" json:"ai_response"`
	DecisionsJSON string `gorm:"type:text" json:"decisions_json"`
	Error         string `json:"error"`
}

type PortfolioSnapshot struct {
	ID        uint      `gorm:"primarykey" json:"id"`
	CreatedAt time.Time `json:"created_at"`

	TotalRub       float64 `json:"total_rub"`
	AvailableRub   float64 `json:"available_rub"`
	PositionsCount int     `json:"positions_count"`
	PositionsJSON  string  `gorm:"type:text" json:"positions_json"`
}

type TradeOutcome struct {
	ID                uint      `gorm:"primarykey" json:"id"`
	CreatedAt         time.Time `json:"created_at"`
	TradeID           uint      `gorm:"not null" json:"trade_id"`
	Hypothesis        string    `gorm:"type:text" json:"hypothesis"`
	EntryFeatures     string    `gorm:"type:text" json:"entry_features"`
	ExitReason        string    `json:"exit_reason"`
	Outcome           string    `json:"outcome"`
	PnL               float64   `json:"pnl"`
	HoldDurationHours float64   `json:"hold_duration_hours"`
	WhatHappened      string    `gorm:"type:text" json:"what_happened"`
}

type JournalLesson struct {
	ID             uint      `gorm:"primarykey" json:"id"`
	CreatedAt      time.Time `json:"created_at"`
	ReviewDate     string    `gorm:"not null;index" json:"review_date"`
	LessonsJSON    string    `gorm:"type:text" json:"lessons_json"`
	RawResponse    string    `gorm:"type:text" json:"raw_response"`
	TradesReviewed int       `json:"trades_reviewed"`
}

type PatternEmbedding struct {
	ID            uint      `gorm:"primarykey" json:"id"`
	CreatedAt     time.Time `json:"created_at"`
	TradeID       uint      `gorm:"not null;index" json:"trade_id"`
	Ticker        string    `gorm:"not null;index" json:"ticker"`
	EntryFeatures string    `gorm:"type:text;not null" json:"entry_features"`
	Embedding     []byte    `gorm:"type:blob;not null" json:"-"`
	Outcome       string    `json:"outcome"`
	PnL           float64   `json:"pnl"`
	HoldHours     float64   `json:"hold_hours"`
}

type CycleSnapshot struct {
	ID            uint      `gorm:"primarykey" json:"id"`
	CreatedAt     time.Time `json:"created_at"`
	SnapshotsJSON string    `gorm:"type:text" json:"snapshots_json"`
	FeaturesJSON  string    `gorm:"type:text" json:"features_json"`
	PortfolioJSON string    `gorm:"type:text" json:"portfolio_json"`
	DecisionsJSON string    `gorm:"type:text" json:"decisions_json"`
	NewsJSON      string    `gorm:"type:text" json:"news_json"`
	MarketRegime  string    `json:"market_regime"`
}
