package scheduler

import (
	"context"
	"fmt"
	"time"

	"github.com/camuig/rus-trader/internal/ai"
	"github.com/camuig/rus-trader/internal/journal"
)

func (s *Scheduler) runDailyReview(ctx context.Context) {
	if s.journal == nil || !s.config.Journal.Enabled {
		return
	}
	if !s.journal.NeedsDailyReview(s.loc) {
		return
	}

	outcomes, err := s.journal.GetPendingOutcomes()
	if err != nil {
		s.logger.Error("daily review: get outcomes", "error", err)
		return
	}
	if len(outcomes) < s.config.Journal.MinTradesForReview {
		s.logger.Info("daily review: not enough trades", "count", len(outcomes), "min", s.config.Journal.MinTradesForReview)
		return
	}

	s.logger.Info("running daily journal review", "outcomes", len(outcomes))

	// Build stats
	var wins, losses int
	var winPnL, lossPnL, winHours, lossHours float64
	exitReasons := make(map[string]int)
	var journalOutcomes []ai.JournalOutcome

	for _, o := range outcomes {
		journalOutcomes = append(journalOutcomes, ai.JournalOutcome{
			Hypothesis:    o.Hypothesis,
			EntryFeatures: o.EntryFeatures,
			ExitReason:    o.ExitReason,
			Outcome:       o.Outcome,
			PnL:           o.PnL,
			HoldHours:     o.HoldDurationHours,
			WhatHappened:  o.WhatHappened,
		})
		exitReasons[o.ExitReason]++
		if o.PnL > 0 {
			wins++
			winPnL += o.PnL
			winHours += o.HoldDurationHours
		} else if o.PnL < 0 {
			losses++
			lossPnL += o.PnL
			lossHours += o.HoldDurationHours
		}
	}

	total := wins + losses
	winRate := 0.0
	if total > 0 {
		winRate = float64(wins) / float64(total) * 100
	}

	// Load previous lessons for continuity
	prevLessons, _ := s.journal.LoadLessons(s.config.Journal.MaxLessonAgeDays, s.config.Journal.MaxLessonsInPrompt)
	var prevLessonLines []string
	for _, l := range prevLessons {
		prevLessonLines = append(prevLessonLines, fmt.Sprintf("[%s] %s: %s → %s", l.Confidence, l.Pattern, l.Observation, l.Recommendation))
	}

	reviewReq := &ai.JournalReviewRequest{
		Outcomes: journalOutcomes,
		Stats: ai.JournalReviewStats{
			TotalTrades:      total,
			WinRate:          winRate,
			AvgWinPnL:        safeDiv(winPnL, float64(wins)),
			AvgLossPnL:       safeDiv(lossPnL, float64(losses)),
			AvgHoldHoursWin:  safeDiv(winHours, float64(wins)),
			AvgHoldHoursLoss: safeDiv(lossHours, float64(losses)),
			ByExitReason:     exitReasons,
		},
		PreviousLessons: prevLessonLines,
		CurrentDate:     time.Now().In(s.loc),
	}

	rawLessons, rawResponse, err := s.ai.JournalReview(ctx, reviewReq)
	if err != nil {
		s.logger.Error("daily review: LLM call", "error", err)
		return
	}

	// Convert raw lessons to journal.Lesson
	var lessons []journal.Lesson
	for _, raw := range rawLessons {
		l := journal.Lesson{}
		if v, ok := raw["pattern"].(string); ok {
			l.Pattern = v
		}
		if v, ok := raw["observation"].(string); ok {
			l.Observation = v
		}
		if v, ok := raw["recommendation"].(string); ok {
			l.Recommendation = v
		}
		if v, ok := raw["confidence"].(string); ok {
			l.Confidence = v
		}
		if arr, ok := raw["tickers"].([]interface{}); ok {
			for _, t := range arr {
				if str, ok := t.(string); ok {
					l.Tickers = append(l.Tickers, str)
				}
			}
		}
		if l.Pattern != "" {
			lessons = append(lessons, l)
		}
	}

	prevDay := journal.PreviousTradingDay(time.Now().In(s.loc))
	if err := s.journal.SaveReview(prevDay.Format("2006-01-02"), lessons, rawResponse, len(outcomes)); err != nil {
		s.logger.Error("daily review: save", "error", err)
		return
	}

	s.logger.Info("daily review complete", "lessons", len(lessons), "trades_reviewed", len(outcomes))
	for _, l := range lessons {
		s.logger.Info("lesson", "confidence", l.Confidence, "pattern", l.Pattern, "recommendation", l.Recommendation)
	}
}
