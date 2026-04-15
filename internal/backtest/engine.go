package backtest

import (
	"fmt"
	"time"

	"github.com/camuig/rus-trader/internal/config"
	"github.com/camuig/rus-trader/internal/logger"
	"github.com/camuig/rus-trader/internal/storage"
)

// defaultConfidence is used for all historical trades because storage.Trade
// does not have a Confidence field. 100 means confidence check always passes
// unless overridden to a higher threshold (which would block everything).
const defaultConfidence = 100.0

type Overrides struct {
	MinConfidence      *int
	MinRiskRewardRatio *float64
	MinStopLossPct     *float64
	MinTakeProfitPct   *float64
	MaxDailyLossRub    *float64
	RequireUptrend     *bool
	MinATRPct          *float64
}

type ResultStats struct {
	Trades      int     `json:"trades"`
	Wins        int     `json:"wins"`
	Losses      int     `json:"losses"`
	WinRate     float64 `json:"win_rate"`
	TotalPnL    float64 `json:"total_pnl"`
	MaxDrawdown float64 `json:"max_drawdown"`
	AvgWinPnL   float64 `json:"avg_win_pnl"`
	AvgLossPnL  float64 `json:"avg_loss_pnl"`
}

type TradeResult struct {
	Ticker      string    `json:"ticker"`
	EntryPrice  float64   `json:"entry_price"`
	ExitPrice   float64   `json:"exit_price"`
	PnL         float64   `json:"pnl"`
	Confidence  float64   `json:"confidence"`
	HoldHours   float64   `json:"hold_hours"`
	RealAllowed bool      `json:"real_allowed"`
	TestAllowed bool      `json:"test_allowed"`
	BlockReason string    `json:"block_reason,omitempty"`
	EntryTime   time.Time `json:"entry_time"`
	ExitTime    time.Time `json:"exit_time"`
}

type BacktestResult struct {
	Period         string            `json:"period"`
	Real           ResultStats       `json:"real"`
	Test           ResultStats       `json:"test"`
	FilteredOut    int               `json:"filtered_out"`
	FilteredOutPnL float64           `json:"filtered_out_pnl"`
	NewAllowed     int               `json:"new_allowed"`
	NewAllowedPnL  float64           `json:"new_allowed_pnl"`
	ParamsChanged  map[string]string `json:"params_changed"`
	Trades         []TradeResult     `json:"trades"`
}

type tradePair struct {
	Ticker        string
	BuyPrice      float64
	SellPrice     float64
	PnL           float64
	Confidence    float64
	EntryFeatures string
	StopLossPct   float64
	TakeProfitPct float64
	EntryTime     time.Time
	ExitTime      time.Time
}

type Engine struct {
	repo   *storage.Repository
	config *config.Config
	logger *logger.Logger
}

func NewEngine(repo *storage.Repository, cfg *config.Config, log *logger.Logger) *Engine {
	return &Engine{repo: repo, config: cfg, logger: log}
}

func (e *Engine) Run(from, to time.Time, overrides Overrides) (*BacktestResult, error) {
	trades, err := e.repo.GetTradesInPeriod(from, to)
	if err != nil {
		return nil, fmt.Errorf("load trades: %w", err)
	}

	pairs := reconstructPairs(trades)
	if len(pairs) == 0 {
		return &BacktestResult{
			Period:        fmt.Sprintf("%s — %s", from.Format("2006-01-02"), to.Format("2006-01-02")),
			ParamsChanged: buildParamsChanged(e.config.Trading, overrides),
		}, nil
	}

	// Build test config with overrides
	testCfg := e.config.Trading
	applyOverrides(&testCfg, overrides)

	realSim := NewGuardSimulator(e.config.Trading)
	testSim := NewGuardSimulator(testCfg)

	var results []TradeResult
	var dailyPnL float64
	var currentDay string

	for _, p := range pairs {
		day := p.EntryTime.Format("2006-01-02")
		if day != currentDay {
			dailyPnL = 0
			currentDay = day
		}

		state := SimStateFromTrade(p.EntryFeatures, dailyPnL, 0)

		var slPct, tpPct float64
		if p.BuyPrice > 0 {
			slPct = p.StopLossPct
			tpPct = p.TakeProfitPct
		}

		realAllowed, _ := realSim.WouldAllow(p.Confidence, slPct, tpPct, state)
		testAllowed, blockReason := testSim.WouldAllow(p.Confidence, slPct, tpPct, state)

		holdHours := p.ExitTime.Sub(p.EntryTime).Hours()

		results = append(results, TradeResult{
			Ticker:      p.Ticker,
			EntryPrice:  p.BuyPrice,
			ExitPrice:   p.SellPrice,
			PnL:         p.PnL,
			Confidence:  p.Confidence,
			HoldHours:   holdHours,
			RealAllowed: realAllowed,
			TestAllowed: testAllowed,
			BlockReason: blockReason,
			EntryTime:   p.EntryTime,
			ExitTime:    p.ExitTime,
		})

		if realAllowed {
			dailyPnL += p.PnL
		}
	}

	real := buildStats(results, func(tr TradeResult) bool { return tr.RealAllowed })
	real.MaxDrawdown = calcMaxDrawdown(results, func(tr TradeResult) bool { return tr.RealAllowed })

	test := buildStats(results, func(tr TradeResult) bool { return tr.TestAllowed })
	test.MaxDrawdown = calcMaxDrawdown(results, func(tr TradeResult) bool { return tr.TestAllowed })

	var filteredOut int
	var filteredOutPnL float64
	var newAllowed int
	var newAllowedPnL float64
	for _, tr := range results {
		if tr.RealAllowed && !tr.TestAllowed {
			filteredOut++
			filteredOutPnL += tr.PnL
		}
		if !tr.RealAllowed && tr.TestAllowed {
			newAllowed++
			newAllowedPnL += tr.PnL
		}
	}

	return &BacktestResult{
		Period:         fmt.Sprintf("%s — %s", from.Format("2006-01-02"), to.Format("2006-01-02")),
		Real:           real,
		Test:           test,
		FilteredOut:    filteredOut,
		FilteredOutPnL: filteredOutPnL,
		NewAllowed:     newAllowed,
		NewAllowedPnL:  newAllowedPnL,
		ParamsChanged:  buildParamsChanged(e.config.Trading, overrides),
		Trades:         results,
	}, nil
}

func reconstructPairs(trades []storage.Trade) []tradePair {
	buyMap := make(map[string]*storage.Trade) // ticker -> latest BUY
	var pairs []tradePair

	for i := range trades {
		t := &trades[i]
		if t.Action == "BUY" {
			buyMap[t.Ticker] = t
		} else if t.Action == "SELL" {
			buy, ok := buyMap[t.Ticker]
			if !ok {
				continue
			}
			var slPct, tpPct float64
			if buy.Price > 0 {
				if buy.StopLossPrice > 0 {
					slPct = (buy.Price - buy.StopLossPrice) / buy.Price * 100
				}
				if buy.TakeProfitPrice > 0 {
					tpPct = (buy.TakeProfitPrice - buy.Price) / buy.Price * 100
				}
			}
			pairs = append(pairs, tradePair{
				Ticker:        t.Ticker,
				BuyPrice:      buy.Price,
				SellPrice:     t.Price,
				PnL:           t.PnL,
				Confidence:    defaultConfidence,
				EntryFeatures: buy.EntryFeatures,
				StopLossPct:   slPct,
				TakeProfitPct: tpPct,
				EntryTime:     buy.CreatedAt,
				ExitTime:      t.CreatedAt,
			})
			delete(buyMap, t.Ticker)
		}
	}
	return pairs
}

func buildStats(trades []TradeResult, filter func(TradeResult) bool) ResultStats {
	var s ResultStats
	for _, t := range trades {
		if !filter(t) {
			continue
		}
		s.Trades++
		s.TotalPnL += t.PnL
		if t.PnL > 0 {
			s.Wins++
			s.AvgWinPnL += t.PnL
		} else if t.PnL < 0 {
			s.Losses++
			s.AvgLossPnL += t.PnL
		}
	}
	if s.Trades > 0 {
		s.WinRate = float64(s.Wins) / float64(s.Trades) * 100
	}
	if s.Wins > 0 {
		s.AvgWinPnL /= float64(s.Wins)
	}
	if s.Losses > 0 {
		s.AvgLossPnL /= float64(s.Losses)
	}
	return s
}

func calcMaxDrawdown(trades []TradeResult, filter func(TradeResult) bool) float64 {
	var cumPnL, peak, maxDD float64
	for _, t := range trades {
		if !filter(t) {
			continue
		}
		cumPnL += t.PnL
		if cumPnL > peak {
			peak = cumPnL
		}
		dd := peak - cumPnL
		if dd > maxDD {
			maxDD = dd
		}
	}
	return maxDD
}

func applyOverrides(cfg *config.TradingConfig, o Overrides) {
	if o.MinConfidence != nil {
		cfg.MinConfidence = *o.MinConfidence
	}
	if o.MinRiskRewardRatio != nil {
		cfg.MinRiskRewardRatio = *o.MinRiskRewardRatio
	}
	if o.MinStopLossPct != nil {
		cfg.MinStopLossPct = *o.MinStopLossPct
	}
	if o.MinTakeProfitPct != nil {
		cfg.MinTakeProfitPct = *o.MinTakeProfitPct
	}
	if o.MaxDailyLossRub != nil {
		cfg.MaxDailyLossRub = *o.MaxDailyLossRub
	}
	if o.RequireUptrend != nil {
		cfg.RequireUptrend = *o.RequireUptrend
	}
	if o.MinATRPct != nil {
		cfg.MinATRPct = *o.MinATRPct
	}
}

func buildParamsChanged(base config.TradingConfig, o Overrides) map[string]string {
	m := make(map[string]string)
	if o.MinConfidence != nil {
		m["min_confidence"] = fmt.Sprintf("%d → %d", base.MinConfidence, *o.MinConfidence)
	}
	if o.MinRiskRewardRatio != nil {
		m["min_risk_reward_ratio"] = fmt.Sprintf("%.1f → %.1f", base.MinRiskRewardRatio, *o.MinRiskRewardRatio)
	}
	if o.MinStopLossPct != nil {
		m["min_stop_loss_pct"] = fmt.Sprintf("%.1f → %.1f", base.MinStopLossPct, *o.MinStopLossPct)
	}
	if o.MinTakeProfitPct != nil {
		m["min_take_profit_pct"] = fmt.Sprintf("%.1f → %.1f", base.MinTakeProfitPct, *o.MinTakeProfitPct)
	}
	if o.MaxDailyLossRub != nil {
		m["max_daily_loss_rub"] = fmt.Sprintf("%.0f → %.0f", base.MaxDailyLossRub, *o.MaxDailyLossRub)
	}
	if o.RequireUptrend != nil {
		m["require_uptrend"] = fmt.Sprintf("%v → %v", base.RequireUptrend, *o.RequireUptrend)
	}
	if o.MinATRPct != nil {
		m["min_atr_pct"] = fmt.Sprintf("%.1f → %.1f", base.MinATRPct, *o.MinATRPct)
	}
	return m
}
