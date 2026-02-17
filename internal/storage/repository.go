package storage

import (
	"time"

	"gorm.io/gorm"
)

type Repository struct {
	db *gorm.DB
}

func NewRepository(db *gorm.DB) *Repository {
	return &Repository{db: db}
}

// Trades

func (r *Repository) SaveTrade(trade *Trade) error {
	return r.db.Create(trade).Error
}

func (r *Repository) UpdateTrade(trade *Trade) error {
	return r.db.Save(trade).Error
}

func (r *Repository) GetOpenTrades() ([]Trade, error) {
	var trades []Trade
	err := r.db.Where("status = ?", "open").Find(&trades).Error
	return trades, err
}

func (r *Repository) GetOpenTradeByTicker(ticker string) (*Trade, error) {
	var trade Trade
	err := r.db.Where("status = ? AND ticker = ? AND action = ?", "open", ticker, "BUY").
		Order("created_at DESC").First(&trade).Error
	if err != nil {
		return nil, err
	}
	return &trade, nil
}

func (r *Repository) GetRecentTrades(limit int) ([]Trade, error) {
	var trades []Trade
	err := r.db.Order("created_at DESC").Limit(limit).Find(&trades).Error
	return trades, err
}

func (r *Repository) GetTodayPnL() (float64, error) {
	// Use MSK timezone for "today" boundary
	msk, err := time.LoadLocation("Europe/Moscow")
	if err != nil {
		msk = time.FixedZone("MSK", 3*60*60)
	}
	now := time.Now().In(msk)
	todayMSK := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, msk)

	var total float64
	err = r.db.Model(&Trade{}).
		Where("status = ? AND action = ? AND updated_at >= ?", "closed", "SELL", todayMSK).
		Select("COALESCE(SUM(pnl), 0)").Scan(&total).Error
	return total, err
}

func (r *Repository) GetTotalPnL() (float64, error) {
	var total float64
	err := r.db.Model(&Trade{}).
		Where("status = ? AND action = ?", "closed", "SELL").
		Select("COALESCE(SUM(pnl), 0)").Scan(&total).Error
	return total, err
}

func (r *Repository) GetClosedTradesLast24h() ([]Trade, error) {
	cutoff := time.Now().Add(-24 * time.Hour)
	var trades []Trade
	err := r.db.Where("status = ? AND action = ? AND created_at >= ?", "closed", "SELL", cutoff).
		Order("created_at DESC").Find(&trades).Error
	return trades, err
}

// Analysis Logs

func (r *Repository) SaveAnalysisLog(log *AnalysisLog) error {
	return r.db.Create(log).Error
}

// Portfolio Snapshots

func (r *Repository) SavePortfolioSnapshot(snapshot *PortfolioSnapshot) error {
	return r.db.Create(snapshot).Error
}

func (r *Repository) GetLatestSnapshot() (*PortfolioSnapshot, error) {
	var snapshot PortfolioSnapshot
	err := r.db.Order("created_at DESC").First(&snapshot).Error
	if err != nil {
		return nil, err
	}
	return &snapshot, nil
}
