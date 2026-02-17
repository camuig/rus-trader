package storage

import "time"

type Trade struct {
	ID        uint      `gorm:"primarykey" json:"id"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`

	Ticker    string  `gorm:"index;not null" json:"ticker"`
	Action    string  `gorm:"not null" json:"action"` // BUY or SELL
	Price     float64 `gorm:"not null" json:"price"`
	Quantity  int64   `gorm:"not null" json:"quantity"`
	OrderID   string  `json:"order_id"`

	StopLossPrice   float64 `json:"stop_loss_price"`
	TakeProfitPrice float64 `json:"take_profit_price"`
	StopLossOrderID string  `json:"stop_loss_order_id"`
	TakeProfitOrderID string `json:"take_profit_order_id"`

	PnL    float64 `gorm:"column:pnl" json:"pnl"`
	Status string  `gorm:"not null;default:'open'" json:"status"` // open, closed
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
