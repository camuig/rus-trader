package web

import (
	"html/template"
	"net/http"
	"time"

	"github.com/camuig/rus-trader/internal/storage"
)

type OpenPosition struct {
	Ticker          string
	Price           float64
	Quantity        int64
	StopLossPrice   float64
	TakeProfitPrice float64
	CreatedAt       time.Time
	CurrentPrice    float64
	PnL             float64
	PnLPercent      float64
}

type DashboardData struct {
	TotalRub       float64
	AvailableRub   float64
	DailyPnL       float64
	TotalPnL       float64
	OpenPositions  []OpenPosition
	RecentTrades   []storage.Trade
	PositionsCount int
	Mode           string
}

func (s *Server) handleDashboard(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}

	data := DashboardData{}

	// Get portfolio snapshot
	snapshot, err := s.repo.GetLatestSnapshot()
	if err == nil && snapshot != nil {
		data.TotalRub = snapshot.TotalRub
		data.AvailableRub = snapshot.AvailableRub
		data.PositionsCount = snapshot.PositionsCount
	}

	// Get PnL
	if dailyPnL, err := s.repo.GetTodayPnL(); err == nil {
		data.DailyPnL = dailyPnL
	}
	if totalPnL, err := s.repo.GetTotalPnL(); err == nil {
		data.TotalPnL = totalPnL
	}

	// Get open positions and enrich with live prices
	if positions, err := s.repo.GetOpenTrades(); err == nil {
		data.OpenPositions = s.enrichPositions(positions)
	}

	// Get recent trades
	if trades, err := s.repo.GetRecentTrades(20); err == nil {
		data.RecentTrades = trades
	}

	// Mode
	if s.config.IsSandbox() {
		data.Mode = "SANDBOX"
	} else {
		data.Mode = "LIVE"
	}

	tmpl, err := template.ParseFiles("templates/dashboard.html")
	if err != nil {
		s.logger.Error("parse template", "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := tmpl.Execute(w, data); err != nil {
		s.logger.Error("execute template", "error", err)
	}
}

func (s *Server) enrichPositions(trades []storage.Trade) []OpenPosition {
	// Build a map of ticker -> live position data from broker
	type liveData struct {
		CurrentPrice float64
		PnL          float64
	}
	liveMap := make(map[string]liveData)

	portfolio, err := s.broker.GetPortfolio()
	if err != nil {
		s.logger.Error("get portfolio for dashboard", "error", err)
	} else {
		for _, pos := range portfolio.Positions {
			if pos.Ticker != "" {
				liveMap[pos.Ticker] = liveData{
					CurrentPrice: pos.CurrentPrice,
					PnL:          pos.PnL,
				}
			}
		}
	}

	result := make([]OpenPosition, 0, len(trades))
	for _, t := range trades {
		op := OpenPosition{
			Ticker:          t.Ticker,
			Price:           t.Price,
			Quantity:        t.Quantity,
			StopLossPrice:   t.StopLossPrice,
			TakeProfitPrice: t.TakeProfitPrice,
			CreatedAt:       t.CreatedAt,
		}
		if live, ok := liveMap[t.Ticker]; ok {
			op.CurrentPrice = live.CurrentPrice
			op.PnL = live.CurrentPrice*float64(t.Quantity) - t.Price*float64(t.Quantity)
			if t.Price > 0 {
				op.PnLPercent = (live.CurrentPrice - t.Price) / t.Price * 100
			}
		}
		result = append(result, op)
	}
	return result
}
