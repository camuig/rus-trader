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

func (r *Repository) GetLastSellTime(ticker string) (time.Time, error) {
	var trade Trade
	err := r.db.Where("ticker = ? AND action = ? AND status = ?", ticker, "SELL", "closed").
		Order("created_at DESC").First(&trade).Error
	if err != nil {
		return time.Time{}, err
	}
	return trade.CreatedAt, nil
}

func (r *Repository) CountTodayTrades() (int, error) {
	msk, err := time.LoadLocation("Europe/Moscow")
	if err != nil {
		msk = time.FixedZone("MSK", 3*60*60)
	}
	now := time.Now().In(msk)
	todayMSK := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, msk)

	var count int64
	err = r.db.Model(&Trade{}).
		Where("action = ? AND created_at >= ?", "BUY", todayMSK).
		Count(&count).Error
	return int(count), err
}

func (r *Repository) CountOpenPositions() (int, error) {
	var count int64
	err := r.db.Model(&Trade{}).
		Where("status = ? AND action = ?", "open", "BUY").
		Count(&count).Error
	return int(count), err
}

func (r *Repository) GetTodayTradedTickers() ([]string, error) {
	msk, err := time.LoadLocation("Europe/Moscow")
	if err != nil {
		msk = time.FixedZone("MSK", 3*60*60)
	}
	now := time.Now().In(msk)
	todayMSK := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, msk)

	var tickers []string
	err = r.db.Model(&Trade{}).
		Where("created_at >= ?", todayMSK).
		Distinct("ticker").Pluck("ticker", &tickers).Error
	return tickers, err
}

// Performance Stats

type PerformanceStats7d struct {
	WinRate      float64
	AvgProfit    float64
	AvgLoss      float64
	TotalPnL     float64
	TradeCount   int
	WorstTickers []string
}

func (r *Repository) GetPerformanceStats7d() (PerformanceStats7d, error) {
	cutoff := time.Now().Add(-7 * 24 * time.Hour)

	var trades []Trade
	err := r.db.Where("status = ? AND action = ? AND created_at >= ?", "closed", "SELL", cutoff).
		Find(&trades).Error
	if err != nil {
		return PerformanceStats7d{}, err
	}

	if len(trades) == 0 {
		return PerformanceStats7d{}, nil
	}

	var wins, losses int
	var totalProfit, totalLoss, totalPnL float64
	tickerPnL := make(map[string]float64)

	for _, t := range trades {
		totalPnL += t.PnL
		tickerPnL[t.Ticker] += t.PnL
		if t.PnL > 0 {
			wins++
			totalProfit += t.PnL
		} else {
			losses++
			totalLoss += t.PnL
		}
	}

	stats := PerformanceStats7d{
		WinRate:    float64(wins) / float64(len(trades)) * 100,
		TotalPnL:   totalPnL,
		TradeCount: len(trades),
	}
	if wins > 0 {
		stats.AvgProfit = totalProfit / float64(wins)
	}
	if losses > 0 {
		stats.AvgLoss = totalLoss / float64(losses)
	}

	// Find worst tickers (up to 3)
	type tickerScore struct {
		ticker string
		pnl    float64
	}
	var scores []tickerScore
	for t, p := range tickerPnL {
		if p < 0 {
			scores = append(scores, tickerScore{t, p})
		}
	}
	for i := 0; i < len(scores); i++ {
		for j := i + 1; j < len(scores); j++ {
			if scores[j].pnl < scores[i].pnl {
				scores[i], scores[j] = scores[j], scores[i]
			}
		}
	}
	for i := 0; i < len(scores) && i < 3; i++ {
		stats.WorstTickers = append(stats.WorstTickers, scores[i].ticker)
	}

	return stats, nil
}

// GetLosingStreak returns the number of consecutive losing trades (most recent first).
func (r *Repository) GetLosingStreak() (int, error) {
	var trades []Trade
	err := r.db.Where("status = ? AND action = ?", "closed", "SELL").
		Order("created_at DESC").Limit(50).Find(&trades).Error
	if err != nil {
		return 0, err
	}

	streak := 0
	for _, t := range trades {
		if t.PnL >= 0 {
			break
		}
		streak++
	}
	return streak, nil
}

// GetLosingStreakTickers returns tickers that have N or more consecutive losing SELL
// trades at the end of their history, within the last `windowDays` days.
// Older trades are ignored so the block naturally expires over time.
// If windowDays <= 0, the streak window is unlimited.
func (r *Repository) GetLosingStreakTickers(minStreak int, windowDays int) (map[string]int, error) {
	if minStreak <= 0 {
		return nil, nil
	}
	query := r.db.Where("status = ? AND action = ?", "closed", "SELL")
	if windowDays > 0 {
		cutoff := time.Now().Add(-time.Duration(windowDays) * 24 * time.Hour)
		query = query.Where("created_at >= ?", cutoff)
	}
	var trades []Trade
	if err := query.Order("created_at DESC").Limit(300).Find(&trades).Error; err != nil {
		return nil, err
	}
	// Walk from most recent; track per-ticker streak until a non-losing trade breaks it.
	streaks := make(map[string]int)
	broken := make(map[string]bool)
	for _, t := range trades {
		if broken[t.Ticker] {
			continue
		}
		if t.PnL < 0 {
			streaks[t.Ticker]++
		} else {
			broken[t.Ticker] = true
		}
	}
	result := make(map[string]int)
	for ticker, s := range streaks {
		if s >= minStreak {
			result[ticker] = s
		}
	}
	return result, nil
}

// GetRecentLossTickers returns tickers that had losing trades in the last N days.
func (r *Repository) GetRecentLossTickers(days int) ([]string, error) {
	cutoff := time.Now().Add(-time.Duration(days) * 24 * time.Hour)
	var tickers []string
	err := r.db.Model(&Trade{}).
		Where("status = ? AND action = ? AND pnl < 0 AND created_at >= ?", "closed", "SELL", cutoff).
		Distinct("ticker").Pluck("ticker", &tickers).Error
	return tickers, err
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

// Trade Outcomes

func (r *Repository) SaveTradeOutcome(outcome *TradeOutcome) error {
	return r.db.Create(outcome).Error
}

func (r *Repository) GetOutcomesSinceLastReview() ([]TradeOutcome, error) {
	var lastLesson JournalLesson
	hasLast := r.db.Order("review_date DESC").First(&lastLesson).Error == nil

	var outcomes []TradeOutcome
	query := r.db.Order("created_at DESC")
	if hasLast {
		query = query.Where("created_at > ?", lastLesson.CreatedAt)
	}
	err := query.Limit(50).Find(&outcomes).Error
	return outcomes, err
}

func (r *Repository) GetOutcomesForDate(date string) ([]TradeOutcome, error) {
	var outcomes []TradeOutcome
	err := r.db.Where("DATE(created_at) = ?", date).
		Order("created_at DESC").Find(&outcomes).Error
	return outcomes, err
}

// Journal Lessons

func (r *Repository) SaveJournalLesson(lesson *JournalLesson) error {
	return r.db.Create(lesson).Error
}

func (r *Repository) GetLatestLessons(maxAgeDays int, maxCount int) ([]JournalLesson, error) {
	cutoff := time.Now().AddDate(0, 0, -maxAgeDays)
	var lessons []JournalLesson
	err := r.db.Where("created_at >= ?", cutoff).
		Order("created_at DESC").Limit(maxCount).Find(&lessons).Error
	return lessons, err
}

func (r *Repository) HasReviewForDate(date string) (bool, error) {
	var count int64
	err := r.db.Model(&JournalLesson{}).Where("review_date = ?", date).Count(&count).Error
	return count > 0, err
}

// Pattern Embeddings

func (r *Repository) SavePatternEmbedding(pe *PatternEmbedding) error {
	return r.db.Create(pe).Error
}

func (r *Repository) UpdatePatternOutcome(tradeID uint, outcome string, pnl float64, holdHours float64) error {
	return r.db.Model(&PatternEmbedding{}).Where("trade_id = ?", tradeID).
		Updates(map[string]interface{}{
			"outcome":    outcome,
			"pnl":        pnl,
			"hold_hours": holdHours,
		}).Error
}

func (r *Repository) GetAllClosedEmbeddings() ([]PatternEmbedding, error) {
	var patterns []PatternEmbedding
	err := r.db.Where("outcome != '' AND outcome IS NOT NULL").
		Find(&patterns).Error
	return patterns, err
}

func (r *Repository) GetBuyTradeForSell(ticker string) (*Trade, error) {
	var trade Trade
	err := r.db.Where("ticker = ? AND action = ? AND status = ?", ticker, "BUY", "closed").
		Order("updated_at DESC").First(&trade).Error
	if err != nil {
		return nil, err
	}
	return &trade, nil
}
