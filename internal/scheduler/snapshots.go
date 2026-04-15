package scheduler

import (
	"encoding/json"

	"github.com/camuig/rus-trader/internal/broker"
	"github.com/camuig/rus-trader/internal/storage"
)

func (s *Scheduler) saveAnalysisLog(tickersCount int, rawResponse, decisionsJSON string, err error) {
	log := &storage.AnalysisLog{
		SignalsCount:  tickersCount,
		AIResponse:    rawResponse,
		DecisionsJSON: decisionsJSON,
	}
	if err != nil {
		log.Error = err.Error()
	}
	if dbErr := s.repo.SaveAnalysisLog(log); dbErr != nil {
		s.logger.Error("save analysis log", "error", dbErr)
	}
}

func (s *Scheduler) savePortfolioSnapshot(portfolio *broker.PortfolioInfo) {
	positionsJSON, _ := json.Marshal(portfolio.Positions)
	snapshot := &storage.PortfolioSnapshot{
		TotalRub:       portfolio.TotalRub,
		AvailableRub:   portfolio.AvailableRub,
		PositionsCount: len(portfolio.Positions),
		PositionsJSON:  string(positionsJSON),
	}
	if err := s.repo.SavePortfolioSnapshot(snapshot); err != nil {
		s.logger.Error("save portfolio snapshot", "error", err)
	}
}
