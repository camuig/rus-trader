package journal

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/camuig/rus-trader/internal/logger"
	"github.com/camuig/rus-trader/internal/storage"
)

// Lesson represents a single lesson extracted from a daily review.
type Lesson struct {
	Pattern        string   `json:"pattern"`
	Observation    string   `json:"observation"`
	Recommendation string   `json:"recommendation"`
	Confidence     string   `json:"confidence"` // "low" | "medium" | "high"
	Tickers        []string `json:"tickers,omitempty"`
}

// Journal manages trade outcome recording and lesson loading.
type Journal struct {
	repo   *storage.Repository
	logger *logger.Logger
}

// New creates a new Journal instance.
func New(repo *storage.Repository, log *logger.Logger) *Journal {
	return &Journal{repo: repo, logger: log}
}

// RecordOutcome saves a TradeOutcome for the given buy trade with sell details.
func (j *Journal) RecordOutcome(buyTrade *storage.Trade, sellPrice float64, pnl float64, sellReasoning string) {
	outcome := storage.TradeOutcome{}
	outcome.TradeID = buyTrade.ID
	outcome.Hypothesis = buyTrade.Reasoning
	outcome.EntryFeatures = buyTrade.EntryFeatures
	outcome.PnL = pnl

	switch {
	case pnl > 0:
		outcome.Outcome = "win"
	case pnl < 0:
		outcome.Outcome = "loss"
	default:
		outcome.Outcome = "breakeven"
	}

	holdHours := time.Since(buyTrade.CreatedAt).Hours()
	outcome.HoldDurationHours = holdHours

	outcome.ExitReason = ClassifyExitReason(sellReasoning)
	outcome.WhatHappened = ComputeWhatHappened(buyTrade.Price, sellPrice, holdHours)

	if err := j.repo.SaveTradeOutcome(&outcome); err != nil {
		j.logger.Errorf("journal: failed to save trade outcome for trade %d: %v", buyTrade.ID, err)
	}
}

// NeedsDailyReview returns true if there is no review recorded for the previous trading day.
func (j *Journal) NeedsDailyReview(loc *time.Location) bool {
	now := time.Now().In(loc)
	prevDay := PreviousTradingDay(now)
	date := prevDay.Format("2006-01-02")

	has, err := j.repo.HasReviewForDate(date)
	if err != nil {
		j.logger.Errorf("journal: failed to check review for date %s: %v", date, err)
		return false
	}
	return !has
}

// GetPendingOutcomes returns trade outcomes not yet included in a review.
func (j *Journal) GetPendingOutcomes() ([]storage.TradeOutcome, error) {
	return j.repo.GetOutcomesSinceLastReview()
}

// SaveReview persists a daily review with the given lessons.
func (j *Journal) SaveReview(date string, lessons []Lesson, rawResponse string, tradesReviewed int) error {
	data, err := json.Marshal(lessons)
	if err != nil {
		return fmt.Errorf("journal: failed to marshal lessons: %w", err)
	}

	lesson := &storage.JournalLesson{
		ReviewDate:     date,
		LessonsJSON:    string(data),
		RawResponse:    rawResponse,
		TradesReviewed: tradesReviewed,
	}
	return j.repo.SaveJournalLesson(lesson)
}

// LoadLessons loads recent lessons from the last maxAgeDays days,
// filtered to exclude low-confidence ones, capped at maxCount total.
func (j *Journal) LoadLessons(maxAgeDays int, maxCount int) ([]Lesson, error) {
	journalLessons, err := j.repo.GetLatestLessons(maxAgeDays, 3)
	if err != nil {
		return nil, fmt.Errorf("journal: failed to load lessons: %w", err)
	}

	var result []Lesson
	for _, jl := range journalLessons {
		if jl.LessonsJSON == "" {
			continue
		}
		var lessons []Lesson
		if err := json.Unmarshal([]byte(jl.LessonsJSON), &lessons); err != nil {
			j.logger.Errorf("journal: failed to parse lessons JSON for review %s: %v", jl.ReviewDate, err)
			continue
		}
		for _, l := range lessons {
			if l.Confidence == "low" {
				continue
			}
			result = append(result, l)
			if len(result) >= maxCount {
				return result, nil
			}
		}
	}
	return result, nil
}

// ClassifyExitReason returns a category string based on the sell reasoning text.
func ClassifyExitReason(reasoning string) string {
	lower := strings.ToLower(reasoning)

	if strings.Contains(lower, "стоп-лосс") ||
		strings.Contains(lower, "stop-loss") ||
		strings.Contains(lower, "пробила sl") ||
		strings.Contains(lower, "sl triggered") ||
		strings.Contains(lower, "стоп лосс") {
		return "sl_hit"
	}

	if strings.Contains(lower, "тейк-профит") ||
		strings.Contains(lower, "take profit") ||
		strings.Contains(lower, "take-profit") ||
		strings.Contains(lower, "фиксируем прибыль") ||
		strings.Contains(lower, "tp достигнут") {
		return "tp_reached"
	}

	if strings.Contains(lower, "trailing") {
		return "trailing_stop"
	}

	if strings.Contains(lower, "orphan") ||
		strings.Contains(lower, "reconcile") {
		return "orphan_cleanup"
	}

	return "ai_sell"
}

// ComputeWhatHappened builds a human-readable description of price movement.
func ComputeWhatHappened(entryPrice, exitPrice, holdHours float64) string {
	if entryPrice <= 0 {
		return ""
	}

	changePct := (exitPrice - entryPrice) / entryPrice * 100

	direction := "упала"
	if changePct > 0 {
		direction = "выросла"
	}

	return fmt.Sprintf("Цена %s на %+.2f%% за %.1f ч (вход %.2f → выход %.2f)",
		direction, changePct, holdHours, entryPrice, exitPrice)
}

// PreviousTradingDay returns the most recent weekday strictly before t.
func PreviousTradingDay(t time.Time) time.Time {
	prev := t.AddDate(0, 0, -1)
	for prev.Weekday() == time.Saturday || prev.Weekday() == time.Sunday {
		prev = prev.AddDate(0, 0, -1)
	}
	return prev
}
