# Phase 3: Pipeline Refactoring + Backtesting — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Refactor the monolithic scheduler.go into focused files by responsibility, add cycle snapshot persistence, and build a CLI backtester for what-if parameter comparison.

**Architecture:** No new abstractions — split scheduler methods into files with a shared `cycleState` struct. Backtester replays historical BUY-SELL pairs from SQLite, simulates guard filtering with overridden parameters, reports P&L comparison in text/JSON.

**Tech Stack:** Go 1.22, SQLite/GORM, flag (stdlib)

**Spec:** `docs/superpowers/specs/2026-04-15-phase3-pipeline-backtest-design.md`

---

## Task 1: Storage — CycleSnapshot Model and Repository

**Files:**
- Modify: `internal/storage/models.go`
- Modify: `internal/storage/database.go`
- Modify: `internal/storage/repository.go`

- [ ] **Step 1: Add CycleSnapshot model to models.go**

Add at end of file:

```go
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
```

- [ ] **Step 2: Add to AutoMigrate in database.go**

Add `&CycleSnapshot{}` to the AutoMigrate call.

- [ ] **Step 3: Add repository methods**

```go
func (r *Repository) SaveCycleSnapshot(snap *CycleSnapshot) error {
	return r.db.Create(snap).Error
}

func (r *Repository) GetCycleSnapshots(from, to time.Time) ([]CycleSnapshot, error) {
	var snapshots []CycleSnapshot
	err := r.db.Where("created_at >= ? AND created_at <= ?", from, to).
		Order("created_at ASC").Find(&snapshots).Error
	return snapshots, err
}

func (r *Repository) GetTradesInPeriod(from, to time.Time) ([]Trade, error) {
	var trades []Trade
	err := r.db.Where("created_at >= ? AND created_at <= ? AND status = ?", from, to, "closed").
		Order("created_at ASC").Find(&trades).Error
	return trades, err
}
```

- [ ] **Step 4: Build and commit**

```bash
CGO_ENABLED=1 go build ./...
git add internal/storage/
git commit -m "feat: add CycleSnapshot model and repository methods"
```

---

## Task 2: Scheduler Split — helpers.go

Move pure utility functions out of scheduler.go. This is the safest first step — these functions have no `*Scheduler` receiver (except `isWithinTradingHours`).

**Files:**
- Create: `internal/scheduler/helpers.go`
- Modify: `internal/scheduler/scheduler.go` (remove moved functions)

- [ ] **Step 1: Create `internal/scheduler/helpers.go`**

Move these functions from scheduler.go (cut-paste, exact same code):

- `func safeDiv(a, b float64) float64` (line ~540)
- `func formatDurationSince(t time.Time) string` (line ~547)
- `func computeMarketContext(snapshots []broker.CandleSnapshot) ai.MarketContext` (line ~558)
- `func (s *Scheduler) isWithinTradingHours() bool` (line ~738)

The file needs these imports:
```go
package scheduler

import (
	"fmt"
	"time"

	"github.com/camuig/rus-trader/internal/ai"
	"github.com/camuig/rus-trader/internal/broker"
)
```

- [ ] **Step 2: Remove the moved functions from scheduler.go**

Delete the function bodies from scheduler.go. The functions now live in helpers.go but are in the same package — all callers work unchanged.

- [ ] **Step 3: Build and test**

```bash
CGO_ENABLED=1 go build ./...
CGO_ENABLED=1 go test ./internal/scheduler/ -v
```

All existing tests must pass — `market_context_test.go` tests `computeMarketContext` which is now in helpers.go but same package.

- [ ] **Step 4: Commit**

```bash
git add internal/scheduler/
git commit -m "refactor: move utility functions to scheduler/helpers.go"
```

---

## Task 3: Scheduler Split — reconcile.go + snapshots.go

**Files:**
- Create: `internal/scheduler/reconcile.go`
- Create: `internal/scheduler/snapshots.go`
- Modify: `internal/scheduler/scheduler.go`

- [ ] **Step 1: Create `internal/scheduler/reconcile.go`**

Move `reconcileOrphanTrades` from scheduler.go (currently line ~759). Imports needed:

```go
package scheduler

import (
	"fmt"

	"github.com/camuig/rus-trader/internal/broker"
)
```

- [ ] **Step 2: Create `internal/scheduler/snapshots.go`**

Move `saveAnalysisLog` and `savePortfolioSnapshot` from scheduler.go. Add new `saveCycleSnapshot` method:

```go
package scheduler

import (
	"encoding/json"

	"github.com/camuig/rus-trader/internal/broker"
	"github.com/camuig/rus-trader/internal/executor"
	"github.com/camuig/rus-trader/internal/storage"
)

func (s *Scheduler) saveAnalysisLog(tickersCount int, rawResponse, decisionsJSON string, err error) {
	// ... exact same code as currently in scheduler.go ...
}

func (s *Scheduler) savePortfolioSnapshot(portfolio *broker.PortfolioInfo) {
	// ... exact same code as currently in scheduler.go ...
}

func (s *Scheduler) saveCycleSnapshot(state *cycleState) {
	if state == nil || state.portfolio == nil {
		return
	}

	snapshotsJSON, _ := json.Marshal(state.allSnapshots)
	featuresJSON, _ := json.Marshal(state.featuresMap)
	portfolioJSON, _ := json.Marshal(state.portfolio)
	decisionsJSON := executor.DecisionsToJSON(state.decisions)

	// Combine ticker news + global news
	newsData := map[string]interface{}{
		"ticker_news": state.tickerNews,
		"global_news": state.globalNews,
	}
	newsJSON, _ := json.Marshal(newsData)

	snap := &storage.CycleSnapshot{
		SnapshotsJSON: string(snapshotsJSON),
		FeaturesJSON:  string(featuresJSON),
		PortfolioJSON: string(portfolioJSON),
		DecisionsJSON: decisionsJSON,
		NewsJSON:      string(newsJSON),
		MarketRegime:  state.marketCtx.Regime,
	}
	if err := s.repo.SaveCycleSnapshot(snap); err != nil {
		s.logger.Error("save cycle snapshot", "error", err)
	}
}
```

Note: `saveCycleSnapshot` takes `*cycleState` which will be defined in Task 6 (cycle.go). For now, declare a minimal placeholder type in this file or wait until Task 6. Best approach: create this file with just the two existing methods moved, and add `saveCycleSnapshot` in Task 6.

Actually — move only `saveAnalysisLog` and `savePortfolioSnapshot` now. `saveCycleSnapshot` will be added in Task 6 when `cycleState` is defined.

- [ ] **Step 3: Remove moved functions from scheduler.go**

- [ ] **Step 4: Build, test, commit**

```bash
CGO_ENABLED=1 go build ./...
CGO_ENABLED=1 go test ./internal/scheduler/ -v
git add internal/scheduler/
git commit -m "refactor: move reconcile and snapshot methods to separate files"
```

---

## Task 4: Scheduler Split — data.go

Move data-fetching methods.

**Files:**
- Create: `internal/scheduler/data.go`
- Modify: `internal/scheduler/scheduler.go`

- [ ] **Step 1: Create `internal/scheduler/data.go`**

Move these methods from scheduler.go:

- `func (s *Scheduler) fetchOrderBooks(ctx, snapshots) map[string]*orderbook.OrderBookMetrics` (~line 614)
- `func (s *Scheduler) scoreSentiment(ctx, tickerNews, tickers) map[string]*sentiment.SentimentResult` (~line 653)
- `func (s *Scheduler) fetchPerformanceStats() ai.PerformanceStats` (~line 896)
- `func (s *Scheduler) findSimilarPatterns(ctx, features) map[string][]ai.PatternMatchInfo` (~line 703)

Imports will include: context, fmt, sync, time, and packages for ai, broker, moex, orderbook, sentiment, vectordb.

- [ ] **Step 2: Remove from scheduler.go**

- [ ] **Step 3: Build, test, commit**

```bash
CGO_ENABLED=1 go build ./...
CGO_ENABLED=1 go test ./internal/scheduler/ -v
git add internal/scheduler/
git commit -m "refactor: move data fetching methods to scheduler/data.go"
```

---

## Task 5: Scheduler Split — agents.go

Move AI agent methods.

**Files:**
- Create: `internal/scheduler/agents.go`
- Modify: `internal/scheduler/scheduler.go`

- [ ] **Step 1: Create `internal/scheduler/agents.go`**

Move `runDailyReview` method from scheduler.go (~line 919, ~120 lines). This is the largest single method to move.

Imports needed: context, fmt, time, and packages for ai, journal.

- [ ] **Step 2: Remove from scheduler.go**

- [ ] **Step 3: Build, test, commit**

```bash
CGO_ENABLED=1 go build ./...
CGO_ENABLED=1 go test ./internal/scheduler/ -v
git add internal/scheduler/
git commit -m "refactor: move AI agent methods to scheduler/agents.go"
```

---

## Task 6: Scheduler Split — cycle.go + execution.go (Final Restructure)

This is the core task: introduce `cycleState`, refactor `runCycle()`, move guard/execute logic.

**Files:**
- Create: `internal/scheduler/cycle.go`
- Create: `internal/scheduler/execution.go`
- Modify: `internal/scheduler/scheduler.go` (remove runCycle and trailing stops)
- Modify: `internal/scheduler/snapshots.go` (add saveCycleSnapshot)

- [ ] **Step 1: Create `internal/scheduler/execution.go`**

Move `updateTrailingStops` from scheduler.go. Also extract guard+execute logic that will be called from the new runCycle.

```go
package scheduler

import (
	"fmt"

	"github.com/camuig/rus-trader/internal/ai"
	"github.com/camuig/rus-trader/internal/broker"
	"github.com/camuig/rus-trader/internal/executor"
	"github.com/camuig/rus-trader/internal/indicators"
)

func (s *Scheduler) applyGuardAndExecute(state *cycleState) {
	indicatorsMap := make(map[string]indicators.Indicators, len(state.snapshots))
	for _, snap := range state.snapshots {
		indicatorsMap[snap.Ticker] = snap.Indicators
	}
	s.guard.SetIndicators(indicatorsMap)
	s.executor.SetIndicators(indicatorsMap)

	allowed, blocked := s.guard.Filter(state.decisions)
	for _, b := range blocked {
		s.logger.Info("decision BLOCKED by guard",
			"ticker", b.Decision.Ticker, "action", b.Decision.Action,
			"reason", b.Reason, "confidence", b.Decision.Confidence)
		s.notifier.NotifyBlocked(b.Decision.Ticker, b.Decision.Action, b.Reason)
	}

	allowedDecisions := make([]ai.AIDecision, len(allowed))
	for i, a := range allowed {
		allowedDecisions[i] = a.Decision
	}
	s.logger.Info("guard filter applied", "allowed", len(allowedDecisions), "blocked", len(blocked))

	if len(allowedDecisions) == 0 && len(state.decisions) > 0 {
		s.logger.Info("all decisions blocked or HOLD")
	}

	if s.config.Trading.TrailingStopEnabled {
		s.updateTrailingStops(state.portfolio)
	}

	s.executor.Execute(allowedDecisions)

	s.saveAnalysisLog(len(state.tradableTickers), state.rawResponse, executor.DecisionsToJSON(state.decisions), nil)
	s.savePortfolioSnapshot(state.portfolio)
	s.saveCycleSnapshot(state)

	s.logger.Info("analysis cycle completed")
}

func (s *Scheduler) updateTrailingStops(portfolio *broker.PortfolioInfo) {
	// ... exact same code as currently in scheduler.go ...
}
```

- [ ] **Step 2: Create `internal/scheduler/cycle.go`**

Define `cycleState` and the new refactored `runCycle`:

```go
package scheduler

import (
	"context"
	"fmt"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/camuig/rus-trader/internal/ai"
	"github.com/camuig/rus-trader/internal/broker"
	"github.com/camuig/rus-trader/internal/dividends"
	"github.com/camuig/rus-trader/internal/features"
	"github.com/camuig/rus-trader/internal/moex"
	"github.com/camuig/rus-trader/internal/orderbook"
	"github.com/camuig/rus-trader/internal/screener"
	"github.com/camuig/rus-trader/internal/sentiment"
)

type cycleState struct {
	tradableTickers []string
	allSnapshots    []broker.CandleSnapshot
	snapshots       []broker.CandleSnapshot
	portfolio       *broker.PortfolioInfo
	tickerNews      map[string][]moex.NewsItem
	globalNews      []string
	divMap          map[string]dividends.DividendInfo
	obMetrics       map[string]*orderbook.OrderBookMetrics
	sentimentMap    map[string]*sentiment.SentimentResult
	featuresMap     map[string]string
	featuresList    []string
	patterns        map[string][]ai.PatternMatchInfo
	openContext     map[string]ai.OpenTradeContext
	todayTraded     []string
	stats           ai.PerformanceStats
	marketCtx       ai.MarketContext
	lessonLines     []string
	decisions       []ai.AIDecision
	rawResponse     string
}

func (s *Scheduler) runCycle(ctx context.Context) (ok bool) {
	defer func() {
		if r := recover(); r != nil {
			s.logger.Error("panic in scheduler cycle", "panic", fmt.Sprint(r))
			s.notifier.NotifyError("scheduler panic", fmt.Errorf("%v", r))
			ok = false
		}
	}()

	if !s.isWithinTradingHours() {
		s.logger.Info("outside trading hours, skipping cycle")
		return true
	}

	state := &cycleState{}

	// Phase 1: Collect market data
	if !s.collectData(ctx, state) {
		return false
	}

	// Phase 2: Enrich with order book, sentiment, features, patterns
	s.enrichData(ctx, state)

	// Phase 3: Run AI agents (parallel)
	s.runAgents(ctx, state)

	// Phase 4: Guard filter + execute + save
	s.applyGuardAndExecute(state)

	return true
}
```

Then implement `collectData` and `enrichData` as methods — these contain the logic currently in runCycle lines ~120-370. 

`collectData` handles: fetch top tickers, resolve UIDs, get portfolio, reconcile orphans, fetch candles, screen, fetch news, fetch open context, today traded, stats, market context.

`enrichData` handles: daily review, load lessons, fetch dividends, fetch order books, score sentiment, build features, find patterns.

`runAgents` handles: parallel errgroup for screening + position manager, merge decisions, log breakdown.

These are large methods — copy the exact existing code from the current `runCycle()` body, just organized into three blocks. Each reads/writes `*cycleState` fields instead of local variables.

- [ ] **Step 3: Add saveCycleSnapshot to snapshots.go**

```go
func (s *Scheduler) saveCycleSnapshot(state *cycleState) {
	if state == nil || state.portfolio == nil {
		return
	}

	snapshotsJSON, _ := json.Marshal(state.allSnapshots)
	featuresJSON, _ := json.Marshal(state.featuresMap)
	portfolioJSON, _ := json.Marshal(state.portfolio)
	decisionsJSON := executor.DecisionsToJSON(state.decisions)
	newsData := map[string]interface{}{
		"ticker_news": state.tickerNews,
		"global_news": state.globalNews,
	}
	newsJSON, _ := json.Marshal(newsData)

	snap := &storage.CycleSnapshot{
		SnapshotsJSON: string(snapshotsJSON),
		FeaturesJSON:  string(featuresJSON),
		PortfolioJSON: string(portfolioJSON),
		DecisionsJSON: decisionsJSON,
		NewsJSON:      string(newsJSON),
		MarketRegime:  state.marketCtx.Regime,
	}
	if err := s.repo.SaveCycleSnapshot(snap); err != nil {
		s.logger.Error("save cycle snapshot", "error", err)
	}
}
```

Add imports for `executor` and `storage` packages to snapshots.go.

- [ ] **Step 4: Remove old runCycle and updateTrailingStops from scheduler.go**

After moving everything, `scheduler.go` should contain ONLY:
- Package declaration and imports
- `Scheduler` struct
- `NewScheduler`
- `Run`
- `runWithRetry`

~80 lines total.

- [ ] **Step 5: Build, test, commit**

```bash
CGO_ENABLED=1 go build ./...
CGO_ENABLED=1 go test ./internal/scheduler/ -v
git add internal/scheduler/
git commit -m "refactor: split runCycle into cycle.go, execution.go; add cycleState and saveCycleSnapshot"
```

---

## Task 7: Backtest — Guard Simulator and Feature Parsing

**Files:**
- Create: `internal/backtest/guard_sim.go`
- Create: `internal/backtest/guard_sim_test.go`

- [ ] **Step 1: Write tests in `internal/backtest/guard_sim_test.go`**

```go
package backtest

import (
	"testing"

	"github.com/camuig/rus-trader/internal/config"
)

func TestParseFeatures_Uptrend(t *testing.T) {
	f := ParseFeatures("SBER (305.20): аптренд (EMA +0.8%), объём повышен (1.8x), RSI 58 — нейтральный, ATR 3.2%.")
	if !f.IsUptrend {
		t.Error("expected uptrend")
	}
	if f.ATRPct < 3.1 || f.ATRPct > 3.3 {
		t.Errorf("expected ATR ~3.2, got %.1f", f.ATRPct)
	}
	if f.RSI < 57 || f.RSI > 59 {
		t.Errorf("expected RSI ~58, got %.0f", f.RSI)
	}
}

func TestParseFeatures_Downtrend(t *testing.T) {
	f := ParseFeatures("GAZP (150.00): даунтренд (EMA -1.2%), RSI 35 — низкий, ATR 2.1%.")
	if f.IsUptrend {
		t.Error("expected downtrend")
	}
	if f.ATRPct < 2.0 || f.ATRPct > 2.2 {
		t.Errorf("expected ATR ~2.1, got %.1f", f.ATRPct)
	}
}

func TestParseFeatures_Empty(t *testing.T) {
	f := ParseFeatures("")
	if f.IsUptrend || f.ATRPct != 0 || f.RSI != 0 {
		t.Error("expected zeros for empty features")
	}
}

func TestWouldAllow_ConfidenceFilter(t *testing.T) {
	cfg := config.TradingConfig{MinConfidence: 70}
	gs := NewGuardSimulator(cfg)
	state := SimState{}

	allowed, reason := gs.WouldAllow(60, 0, 0, state)
	if allowed {
		t.Error("should block confidence 60 < 70")
	}
	if reason == "" {
		t.Error("expected block reason")
	}

	allowed, _ = gs.WouldAllow(80, 0, 0, state)
	if !allowed {
		t.Error("should allow confidence 80 >= 70")
	}
}

func TestWouldAllow_UptrendFilter(t *testing.T) {
	cfg := config.TradingConfig{MinConfidence: 70, RequireUptrend: true}
	gs := NewGuardSimulator(cfg)
	state := SimState{IsUptrend: false, HasIndicators: true}

	allowed, _ := gs.WouldAllow(80, 0, 0, state)
	if allowed {
		t.Error("should block downtrend when RequireUptrend=true")
	}

	state.IsUptrend = true
	allowed, _ = gs.WouldAllow(80, 0, 0, state)
	if !allowed {
		t.Error("should allow uptrend")
	}
}

func TestWouldAllow_RSIFilter(t *testing.T) {
	cfg := config.TradingConfig{MinConfidence: 70}
	gs := NewGuardSimulator(cfg)
	state := SimState{RSI: 85, HasIndicators: true}

	allowed, _ := gs.WouldAllow(80, 0, 0, state)
	if allowed {
		t.Error("should block RSI > 80")
	}
}

func TestWouldAllow_DailyLoss(t *testing.T) {
	cfg := config.TradingConfig{MinConfidence: 70, MaxDailyLossRub: 500}
	gs := NewGuardSimulator(cfg)
	state := SimState{DailyPnL: -600}

	allowed, _ := gs.WouldAllow(80, 0, 0, state)
	if allowed {
		t.Error("should block when daily loss exceeds limit")
	}
}
```

- [ ] **Step 2: Implement `internal/backtest/guard_sim.go`**

```go
package backtest

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/camuig/rus-trader/internal/config"
)

type ParsedFeatures struct {
	IsUptrend     bool
	ATRPct        float64
	RSI           float64
	HasIndicators bool // true if any indicator was parsed
}

type SimState struct {
	OpenPositions int
	DailyPnL      float64
	DailyTrades   int
	IsUptrend     bool
	ATRPct        float64
	RSI           float64
	HasIndicators bool
}

type GuardSimulator struct {
	config config.TradingConfig
}

func NewGuardSimulator(cfg config.TradingConfig) *GuardSimulator {
	return &GuardSimulator{config: cfg}
}

var (
	atrRegex = regexp.MustCompile(`ATR (\d+\.?\d*)%`)
	rsiRegex = regexp.MustCompile(`RSI (\d+\.?\d*)`)
)

func ParseFeatures(text string) ParsedFeatures {
	var f ParsedFeatures
	if text == "" {
		return f
	}

	lower := strings.ToLower(text)
	f.IsUptrend = strings.Contains(lower, "аптренд")
	if strings.Contains(lower, "аптренд") || strings.Contains(lower, "даунтренд") || strings.Contains(lower, "флэт") {
		f.HasIndicators = true
	}

	if m := atrRegex.FindStringSubmatch(text); len(m) > 1 {
		if v, err := strconv.ParseFloat(m[1], 64); err == nil {
			f.ATRPct = v
			f.HasIndicators = true
		}
	}

	if m := rsiRegex.FindStringSubmatch(text); len(m) > 1 {
		if v, err := strconv.ParseFloat(m[1], 64); err == nil {
			f.RSI = v
			f.HasIndicators = true
		}
	}

	return f
}

// WouldAllow checks if a BUY trade with given confidence, SL/TP pcts would pass guard.
func (gs *GuardSimulator) WouldAllow(confidence float64, slPct float64, tpPct float64, state SimState) (bool, string) {
	cfg := gs.config

	if int(confidence) < cfg.MinConfidence {
		return false, fmt.Sprintf("confidence %.0f < %d", confidence, cfg.MinConfidence)
	}

	if cfg.RequireUptrend && state.HasIndicators && !state.IsUptrend {
		return false, "downtrend (RequireUptrend=true)"
	}

	if state.HasIndicators && state.RSI > 80 {
		return false, fmt.Sprintf("RSI %.0f > 80", state.RSI)
	}

	if cfg.MinATRPct > 0 && state.HasIndicators && state.ATRPct > 0 && state.ATRPct < cfg.MinATRPct {
		return false, fmt.Sprintf("ATR %.1f%% < %.1f%%", state.ATRPct, cfg.MinATRPct)
	}

	if cfg.MaxDailyLossRub > 0 && state.DailyPnL < -cfg.MaxDailyLossRub {
		return false, fmt.Sprintf("daily loss %.0f > limit %.0f", -state.DailyPnL, cfg.MaxDailyLossRub)
	}

	if slPct > 0 && tpPct > 0 && cfg.MinRiskRewardRatio > 0 {
		rr := tpPct / slPct
		if rr < cfg.MinRiskRewardRatio {
			return false, fmt.Sprintf("R:R %.1f < %.1f", rr, cfg.MinRiskRewardRatio)
		}
	}

	return true, ""
}

// SimStateFromTrade builds SimState from a trade's EntryFeatures.
func SimStateFromTrade(entryFeatures string, dailyPnL float64, openPositions int) SimState {
	pf := ParseFeatures(entryFeatures)
	return SimState{
		OpenPositions: openPositions,
		DailyPnL:      dailyPnL,
		IsUptrend:     pf.IsUptrend,
		ATRPct:        pf.ATRPct,
		RSI:           pf.RSI,
		HasIndicators: pf.HasIndicators,
	}
}
```

- [ ] **Step 3: Run tests and commit**

```bash
CGO_ENABLED=1 go test ./internal/backtest/ -v
git add internal/backtest/
git commit -m "feat: add backtest guard simulator with feature parsing"
```

---

## Task 8: Backtest — Engine and Report

**Files:**
- Create: `internal/backtest/engine.go`
- Create: `internal/backtest/report.go`
- Create: `internal/backtest/engine_test.go`

- [ ] **Step 1: Write tests in `internal/backtest/engine_test.go`**

```go
package backtest

import (
	"testing"
	"time"

	"github.com/camuig/rus-trader/internal/storage"
)

func TestCalcMaxDrawdown(t *testing.T) {
	trades := []TradeResult{
		{PnL: 100, TestAllowed: true},
		{PnL: -50, TestAllowed: true},
		{PnL: -80, TestAllowed: true},
		{PnL: 200, TestAllowed: true},
	}
	dd := calcMaxDrawdown(trades, func(tr TradeResult) bool { return tr.TestAllowed })
	// Cumulative: 100, 50, -30, 170. Peak=100, trough=-30, DD=130
	if dd < 129 || dd > 131 {
		t.Errorf("expected drawdown ~130, got %.0f", dd)
	}
}

func TestCalcMaxDrawdown_NoTrades(t *testing.T) {
	dd := calcMaxDrawdown(nil, func(tr TradeResult) bool { return true })
	if dd != 0 {
		t.Errorf("expected 0 for empty trades, got %.0f", dd)
	}
}

func TestBuildStats(t *testing.T) {
	trades := []TradeResult{
		{PnL: 100, TestAllowed: true},
		{PnL: -50, TestAllowed: true},
		{PnL: 200, TestAllowed: true},
		{PnL: -30, TestAllowed: false}, // filtered out
	}
	stats := buildStats(trades, func(tr TradeResult) bool { return tr.TestAllowed })
	if stats.Trades != 3 {
		t.Errorf("expected 3 trades, got %d", stats.Trades)
	}
	if stats.Wins != 2 {
		t.Errorf("expected 2 wins, got %d", stats.Wins)
	}
	if stats.TotalPnL != 250 {
		t.Errorf("expected PnL 250, got %.0f", stats.TotalPnL)
	}
}

func TestReconstructPairs(t *testing.T) {
	now := time.Now()
	trades := []storage.Trade{
		{Ticker: "SBER", Action: "BUY", Price: 300, Quantity: 10, StopLossPrice: 290, TakeProfitPrice: 320, EntryFeatures: "test", CreatedAt: now},
		{Ticker: "SBER", Action: "SELL", Price: 310, PnL: 100, CreatedAt: now.Add(time.Hour)},
		{Ticker: "GAZP", Action: "BUY", Price: 150, Quantity: 20, EntryFeatures: "аптренд, RSI 60, ATR 2.5%", CreatedAt: now},
		{Ticker: "GAZP", Action: "SELL", Price: 145, PnL: -100, CreatedAt: now.Add(2 * time.Hour)},
	}
	pairs := reconstructPairs(trades)
	if len(pairs) != 2 {
		t.Fatalf("expected 2 pairs, got %d", len(pairs))
	}
	if pairs[0].Ticker != "SBER" || pairs[0].PnL != 100 {
		t.Errorf("unexpected first pair: %+v", pairs[0])
	}
}
```

- [ ] **Step 2: Implement `internal/backtest/engine.go`**

```go
package backtest

import (
	"fmt"
	"time"

	"github.com/camuig/rus-trader/internal/config"
	"github.com/camuig/rus-trader/internal/logger"
	"github.com/camuig/rus-trader/internal/storage"
)

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
	Trades      int
	Wins        int
	Losses      int
	WinRate     float64
	TotalPnL    float64
	MaxDrawdown float64
	AvgWinPnL   float64
	AvgLossPnL  float64
}

type TradeResult struct {
	Ticker      string
	EntryPrice  float64
	ExitPrice   float64
	PnL         float64
	Confidence  float64
	HoldHours   float64
	RealAllowed bool
	TestAllowed bool
	BlockReason string
	EntryTime   time.Time
	ExitTime    time.Time
}

type BacktestResult struct {
	Period         string
	Real           ResultStats
	Test           ResultStats
	FilteredOut    int
	FilteredOutPnL float64
	NewAllowed     int
	NewAllowedPnL  float64
	ParamsChanged  map[string]string
	Trades         []TradeResult
}

type tradePair struct {
	Ticker         string
	BuyPrice       float64
	SellPrice      float64
	PnL            float64
	Confidence     float64
	EntryFeatures  string
	StopLossPct    float64
	TakeProfitPct  float64
	EntryTime      time.Time
	ExitTime       time.Time
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
	buyMap := make(map[string]*storage.Trade) // ticker → latest BUY
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
				Confidence:    buy.Confidence,
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
```

- [ ] **Step 3: Implement `internal/backtest/report.go`**

```go
package backtest

import (
	"encoding/json"
	"fmt"
	"strings"
)

func FormatText(r *BacktestResult) string {
	var sb strings.Builder

	sb.WriteString(fmt.Sprintf("=== Backtest: %s ===\n\n", r.Period))

	if len(r.ParamsChanged) > 0 {
		sb.WriteString("Parameters changed:\n")
		for k, v := range r.ParamsChanged {
			sb.WriteString(fmt.Sprintf("  %s: %s\n", k, v))
		}
		sb.WriteString("\n")
	} else {
		sb.WriteString("Parameters: unchanged (baseline)\n\n")
	}

	sb.WriteString("Real results:\n")
	sb.WriteString(formatStats(r.Real))
	sb.WriteString("\n")

	sb.WriteString("Test results:\n")
	sb.WriteString(formatStats(r.Test))
	sb.WriteString("\n")

	sb.WriteString("Impact:\n")
	sb.WriteString(fmt.Sprintf("  Filtered out: %d trades (their P&L: %+.0f ₽)\n", r.FilteredOut, r.FilteredOutPnL))
	sb.WriteString(fmt.Sprintf("  Newly allowed: %d trades (their P&L: %+.0f ₽)\n", r.NewAllowed, r.NewAllowedPnL))
	sb.WriteString("\n")

	diff := r.Test.TotalPnL - r.Real.TotalPnL
	verdict := "SAME"
	if diff > 0 {
		verdict = fmt.Sprintf("BETTER by %+.0f ₽", diff)
	} else if diff < 0 {
		verdict = fmt.Sprintf("WORSE by %.0f ₽", diff)
	}
	sb.WriteString(fmt.Sprintf("Verdict: Test params %s\n", verdict))

	return sb.String()
}

func formatStats(s ResultStats) string {
	return fmt.Sprintf("  Trades: %d | Wins: %d | Losses: %d | WR: %.0f%% | P&L: %+.0f ₽\n  Max DD: %.0f ₽ | Avg win: %+.0f ₽ | Avg loss: %.0f ₽\n",
		s.Trades, s.Wins, s.Losses, s.WinRate, s.TotalPnL,
		s.MaxDrawdown, s.AvgWinPnL, s.AvgLossPnL)
}

func FormatJSON(r *BacktestResult) ([]byte, error) {
	return json.MarshalIndent(r, "", "  ")
}
```

- [ ] **Step 4: Run tests and commit**

```bash
CGO_ENABLED=1 go test ./internal/backtest/ -v
git add internal/backtest/
git commit -m "feat: add backtest engine with trade replay and text/JSON reports"
```

---

## Task 9: Backtest CLI

**Files:**
- Create: `cmd/backtest/main.go`

- [ ] **Step 1: Create `cmd/backtest/main.go`**

```go
package main

import (
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/camuig/rus-trader/internal/backtest"
	"github.com/camuig/rus-trader/internal/config"
	"github.com/camuig/rus-trader/internal/logger"
	"github.com/camuig/rus-trader/internal/storage"
)

func main() {
	configPath := flag.String("config", "config.yaml", "path to config file")
	dbPath := flag.String("db", "data/rus-trader.db", "path to SQLite database")
	fromStr := flag.String("from", "", "start date (YYYY-MM-DD), default: 30 days ago")
	toStr := flag.String("to", "", "end date (YYYY-MM-DD), default: today")
	jsonOutput := flag.Bool("json", false, "output as JSON")

	// Parameter overrides
	minConf := flag.Int("min-confidence", 0, "override MinConfidence (0=use config)")
	minRR := flag.Float64("min-rr", 0, "override MinRiskRewardRatio (0=use config)")
	minSL := flag.Float64("min-sl", 0, "override MinStopLossPct (0=use config)")
	minTP := flag.Float64("min-tp", 0, "override MinTakeProfitPct (0=use config)")
	maxDailyLoss := flag.Float64("max-daily-loss", 0, "override MaxDailyLossRub (0=use config)")
	requireUptrend := flag.String("require-uptrend", "", "override RequireUptrend (true/false, empty=use config)")
	minATR := flag.Float64("min-atr", 0, "override MinATRPct (0=use config)")

	flag.Parse()

	cfg, err := config.Load(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "config error: %v\n", err)
		os.Exit(1)
	}

	log := logger.New("error") // quiet

	db, err := storage.NewDatabase(*dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "database error: %v\n", err)
		os.Exit(1)
	}
	repo := storage.NewRepository(db)

	// Parse dates
	now := time.Now()
	from := now.AddDate(0, 0, -30)
	to := now

	if *fromStr != "" {
		from, err = time.Parse("2006-01-02", *fromStr)
		if err != nil {
			fmt.Fprintf(os.Stderr, "invalid --from date: %v\n", err)
			os.Exit(1)
		}
	}
	if *toStr != "" {
		to, err = time.Parse("2006-01-02", *toStr)
		if err != nil {
			fmt.Fprintf(os.Stderr, "invalid --to date: %v\n", err)
			os.Exit(1)
		}
		to = to.Add(23*time.Hour + 59*time.Minute + 59*time.Second)
	}

	// Build overrides
	overrides := backtest.Overrides{}
	if *minConf > 0 {
		overrides.MinConfidence = minConf
	}
	if *minRR > 0 {
		overrides.MinRiskRewardRatio = minRR
	}
	if *minSL > 0 {
		overrides.MinStopLossPct = minSL
	}
	if *minTP > 0 {
		overrides.MinTakeProfitPct = minTP
	}
	if *maxDailyLoss > 0 {
		overrides.MaxDailyLossRub = maxDailyLoss
	}
	if *requireUptrend != "" {
		v := *requireUptrend == "true"
		overrides.RequireUptrend = &v
	}
	if *minATR > 0 {
		overrides.MinATRPct = minATR
	}

	engine := backtest.NewEngine(repo, cfg, log)
	result, err := engine.Run(from, to, overrides)
	if err != nil {
		fmt.Fprintf(os.Stderr, "backtest error: %v\n", err)
		os.Exit(1)
	}

	if result.Real.Trades == 0 && result.Test.Trades == 0 {
		fmt.Println("No trades found in period.")
		return
	}

	if *jsonOutput {
		data, err := backtest.FormatJSON(result)
		if err != nil {
			fmt.Fprintf(os.Stderr, "json error: %v\n", err)
			os.Exit(1)
		}
		fmt.Println(string(data))
	} else {
		fmt.Print(backtest.FormatText(result))
	}
}
```

- [ ] **Step 2: Build and test**

```bash
CGO_ENABLED=1 go build ./cmd/backtest/
# Quick smoke test with current DB
CGO_ENABLED=1 go run ./cmd/backtest/ -config config.yaml -db data/rus-trader.db
```

- [ ] **Step 3: Commit**

```bash
git add cmd/backtest/
git commit -m "feat: add backtest CLI with parameter override and text/JSON output"
```

---

## Task 10: README Update

**Files:**
- Modify: `README.md`

- [ ] **Step 1: Add Phase 3 section**

Add after Phase 2 section:

```markdown
## Phase 3: Pipeline Refactoring + Backtesting

### Scheduler Refactoring
Scheduler разбит на 9 файлов по ответственности (вместо одного ~1000-строчного файла):
- `scheduler.go` — struct, init, run loop
- `cycle.go` — runCycle orchestration + cycleState
- `data.go` — data fetching (order book, sentiment, stats)
- `features.go` — feature building + enrichment
- `agents.go` — AI agents + daily review
- `execution.go` — guard + executor + trailing stops
- `reconcile.go` — orphan trade cleanup
- `snapshots.go` — analysis logs, portfolio/cycle snapshots
- `helpers.go` — utility functions

### Cycle Snapshots
Каждый торговый цикл сохраняет полный snapshot: свечи, features, портфель, решения AI, новости. Это фундамент для будущего full-pipeline backtesting.

### Backtester CLI
```bash
# Базовый replay
go run ./cmd/backtest/ -config config.yaml -db data/rus-trader.db

# Сравнение параметров
go run ./cmd/backtest/ -config config.yaml -db data/rus-trader.db \
  --min-confidence 60 --min-rr 1.2

# JSON output
go run ./cmd/backtest/ ... --json
```

Backtester прогоняет исторические BUY-SELL пары через guard с изменёнными параметрами и показывает: как изменился бы P&L, win rate, drawdown.
```

- [ ] **Step 2: Commit**

```bash
git add README.md
git commit -m "docs: update README with Phase 3 features"
```

---

## Verification Checklist

- [ ] `CGO_ENABLED=1 go build ./...` — clean build
- [ ] `CGO_ENABLED=1 go test ./...` — all tests pass
- [ ] `CGO_ENABLED=1 go vet ./...` — no warnings
- [ ] `wc -l internal/scheduler/*.go` — scheduler.go < 100 lines, total distributed across 9 files
- [ ] `go run ./cmd/backtest/ -config config.yaml -db data/rus-trader.db` — runs and produces output
- [ ] `go run ./cmd/backtest/ -config config.yaml -db data/rus-trader.db --json` — produces valid JSON
- [ ] Start bot normally — verify it still trades correctly after refactoring
