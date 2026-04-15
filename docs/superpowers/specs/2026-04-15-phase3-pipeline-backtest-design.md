# Phase 3: Pipeline Refactoring + Backtesting Framework

**Date:** 2026-04-15
**Status:** Approved
**Scope:** Refactor scheduler into file-per-responsibility, add cycle snapshot persistence, build CLI backtester with guard parameter simulation
**Depends on:** Phase 1 (dual agents, features, journal), Phase 2 (order book, sentiment, vector DB)

## Overview

Phase 3 has two goals:

1. **Pipeline Refactoring** — split the monolithic `scheduler.go` (~1000 lines) into focused files by responsibility. No new abstractions — just clean separation of concerns following Go idioms.

2. **Backtesting Framework** — a CLI tool that replays historical trades from the database, simulates guard filtering with different parameters, and reports what-if P&L comparisons. Also adds cycle snapshot persistence to enable future full-pipeline replay.

### Non-Goals

- Stage interface / formal pipeline abstraction (YAGNI)
- ML models (XGBoost, RL) — insufficient training data
- Historical candle downloading from MOEX
- HTML reports / visualization

## 1. Pipeline Refactoring

### 1.1 Current Problem

`internal/scheduler/scheduler.go` is ~1000 lines with `runCycle()` at ~400 lines containing 17+ sequential steps, plus 12 helper methods. Hard to navigate, hard to test individual steps.

### 1.2 New File Structure

All methods remain on `*Scheduler` — no new types or interfaces. Each file handles one responsibility:

```
internal/scheduler/
  scheduler.go          — Scheduler struct, NewScheduler, Run, runWithRetry
  cycle.go              — runCycle() orchestration (calls methods from other files)
  data.go               — fetchOrderBooks, scoreSentiment, fetchPerformanceStats
  features.go           — buildFeatures (text features + order book + sentiment + vector)
  agents.go             — runScreeningAgent, runPositionManager, runDailyReview
  execution.go          — applyGuardAndExecute, updateTrailingStops
  reconcile.go          — reconcileOrphanTrades
  snapshots.go          — saveAnalysisLog, savePortfolioSnapshot, saveCycleSnapshot
  helpers.go            — computeMarketContext, formatDurationSince, safeDiv, isWithinTradingHours
  market_context_test.go — (existing, unchanged)
```

### 1.3 Cycle State

Instead of passing many variables between steps, introduce a private struct:

```go
// cycleState holds all data accumulated during a single trading cycle.
// Passed between methods to avoid long parameter lists.
type cycleState struct {
    // Data collection
    tradableTickers []string
    allSnapshots    []broker.CandleSnapshot
    snapshots       []broker.CandleSnapshot // after screening
    portfolio       *broker.PortfolioInfo
    tickerNews      map[string][]moex.NewsItem
    globalNews      []string

    // Enrichment
    divMap       map[string]dividends.DividendInfo
    obMetrics    map[string]*orderbook.OrderBookMetrics
    sentimentMap map[string]*sentiment.SentimentResult
    featuresMap  map[string]string
    featuresList []string
    patterns     map[string][]ai.PatternMatchInfo

    // Context
    openContext  map[string]ai.OpenTradeContext
    todayTraded []string
    stats       ai.PerformanceStats
    marketCtx   ai.MarketContext
    lessonLines []string

    // Results
    decisions   []ai.AIDecision
    rawResponse string
}
```

This is not a god-object — it's a data transfer struct scoped to a single cycle. Each method reads/writes specific fields. The struct is never stored or passed across cycle boundaries.

### 1.4 runCycle Refactored

```go
func (s *Scheduler) runCycle(ctx context.Context) bool {
    state := &cycleState{}

    if !s.isWithinTradingHours() {
        s.logger.Info("outside trading hours")
        return true
    }

    // Phase 1: Collect data
    if !s.collectData(ctx, state) {
        return false
    }

    // Phase 2: Enrich with order book, sentiment, features, vector
    s.enrichData(ctx, state)

    // Phase 3: AI agents (parallel)
    s.runAgents(ctx, state)

    // Phase 4: Guard + Execute
    s.applyGuardAndExecute(state)

    // Phase 5: Save state
    s.saveCycleState(state)

    return true
}
```

Each phase method is 30-80 lines. Total `runCycle` is ~15 lines.

### 1.5 Method Distribution

**scheduler.go** (~70 lines):
- `Scheduler` struct, `NewScheduler`, `Run`, `runWithRetry`

**cycle.go** (~40 lines):
- `cycleState` struct definition
- `runCycle` (orchestration only)

**data.go** (~200 lines):
- `collectData` — fetch tickers, candles, portfolio, reconcile, screen, fetch news
- `fetchOrderBooks` — parallel order book fetching (existing)
- `scoreSentiment` — LLM sentiment scoring (existing)
- `fetchPerformanceStats` — stats from DB (existing)

**features.go** (~60 lines):
- `enrichData` — daily review, load lessons, fetch dividends, build features, vector search
- Uses `features.BuildTickerFeatures`, `findSimilarPatterns`

**agents.go** (~200 lines):
- `runAgents` — parallel screening + position manager via errgroup
- `runDailyReview` — journal review (existing)
- `findSimilarPatterns` — vector DB search (existing)

**execution.go** (~100 lines):
- `applyGuardAndExecute` — guard filter, log blocked, executor.Execute
- `updateTrailingStops` (existing)

**reconcile.go** (~40 lines):
- `reconcileOrphanTrades` (existing, moved as-is)

**snapshots.go** (~80 lines):
- `saveCycleState` — calls saveAnalysisLog + savePortfolioSnapshot + saveCycleSnapshot
- `saveAnalysisLog` (existing)
- `savePortfolioSnapshot` (existing)
- `saveCycleSnapshot` (NEW — saves full cycle state for backtesting)

**helpers.go** (~80 lines):
- `computeMarketContext` (existing)
- `isWithinTradingHours` (existing)
- `formatDurationSince` (existing)
- `safeDiv` (existing)

## 2. Cycle Snapshot Persistence

### 2.1 New Model

```go
type CycleSnapshot struct {
    ID            uint      `gorm:"primarykey"`
    CreatedAt     time.Time
    SnapshotsJSON string    `gorm:"type:text"` // serialized candle snapshots with indicators
    FeaturesJSON  string    `gorm:"type:text"` // map[string]string
    PortfolioJSON string    `gorm:"type:text"` // portfolio state
    DecisionsJSON string    `gorm:"type:text"` // AI decisions
    NewsJSON      string    `gorm:"type:text"` // ticker news + global
    MarketRegime  string                        // "uptrend" / "downtrend" / "range" / "mixed"
}
```

### 2.2 When Saved

At the end of each successful `runCycle()`, after all decisions are executed. Serialization is best-effort — errors are logged but don't affect trading.

### 2.3 Repository Methods

```go
func (r *Repository) SaveCycleSnapshot(snap *CycleSnapshot) error
func (r *Repository) GetCycleSnapshots(from, to time.Time) ([]CycleSnapshot, error)
```

## 3. Backtesting Framework

### 3.1 CLI Entry Point

```bash
# Basic replay with current params
go run ./cmd/backtest/ -config config.yaml -db data/rus-trader.db

# Override params
go run ./cmd/backtest/ -config config.yaml -db data/rus-trader.db \
  --min-confidence 60 --min-rr 1.2 --min-sl 2.0

# Date range
go run ./cmd/backtest/ -config config.yaml -db data/rus-trader.db \
  --from 2026-04-01 --to 2026-04-15

# JSON output
go run ./cmd/backtest/ -config config.yaml -db data/rus-trader.db --json
```

**CLI flags:**

| Flag | Type | Description |
|------|------|-------------|
| `--from` | string | Start date (YYYY-MM-DD), default: 30 days ago |
| `--to` | string | End date (YYYY-MM-DD), default: today |
| `--json` | bool | Output as JSON instead of text |
| `--min-confidence` | int | Override MinConfidence |
| `--min-rr` | float | Override MinRiskRewardRatio |
| `--min-sl` | float | Override MinStopLossPct |
| `--min-tp` | float | Override MinTakeProfitPct |
| `--max-daily-loss` | float | Override MaxDailyLossRub |
| `--require-uptrend` | string | Override RequireUptrend ("true"/"false") |
| `--min-atr` | float | Override MinATRPct |

### 3.2 Backtest Engine

```go
// internal/backtest/engine.go

type Engine struct {
    repo   *storage.Repository
    config *config.Config
    logger *logger.Logger
}

type Overrides struct {
    MinConfidence      *int
    MinRiskRewardRatio *float64
    MinStopLossPct     *float64
    MinTakeProfitPct   *float64
    MaxDailyLossRub    *float64
    RequireUptrend     *bool
    MinATRPct          *float64
}

func NewEngine(repo *storage.Repository, cfg *config.Config, log *logger.Logger) *Engine
func (e *Engine) Run(from, to time.Time, overrides Overrides) (*BacktestResult, error)
```

### 3.3 Trade Pair Reconstruction

The engine reconstructs BUY-SELL pairs from the `trades` table:

1. Load all trades in period ordered by created_at.
2. For each BUY with status="closed", find the corresponding SELL (same ticker, next SELL after BUY).
3. Build `TradePair{BuyTrade, SellTrade, PnL, HoldHours}`.

### 3.4 Guard Simulation

```go
// internal/backtest/guard_sim.go

type GuardSimulator struct {
    config    config.TradingConfig // with overrides applied
}

type SimState struct {
    OpenPositions int
    DailyPnL      float64
    DailyTrades   int
    IsUptrend     bool    // parsed from EntryFeatures
    ATRPct        float64 // parsed from EntryFeatures
    RSI           float64 // parsed from EntryFeatures
}

func NewGuardSimulator(baseCfg config.TradingConfig, overrides Overrides) *GuardSimulator
func (gs *GuardSimulator) WouldAllow(trade storage.Trade, state SimState) (bool, string)
```

**WouldAllow** checks (in order):
1. Confidence >= MinConfidence
2. If RequireUptrend and !IsUptrend → blocked
3. If ATRPct > 0 and ATRPct < MinATRPct → blocked
4. If RSI > 80 → blocked
5. SL/TP ratio check against MinRiskRewardRatio
6. DailyPnL check against MaxDailyLossRub

**Feature parsing** from EntryFeatures text:
- `"аптренд"` → IsUptrend=true; `"даунтренд"` / `"флэт"` → false
- `"ATR 2.1%"` → ATRPct=2.1 (regex `ATR (\d+\.?\d*)%`)
- `"RSI 65"` → RSI=65 (regex `RSI (\d+)`)

Features without these patterns → corresponding checks are skipped (conservative: allowed by default).

### 3.5 Result Types

```go
type BacktestResult struct {
    Period string // "2026-04-01 — 2026-04-15"

    // Real results (what actually happened)
    Real ResultStats

    // Test results (with overridden params)
    Test ResultStats

    // Delta
    FilteredOut    int     // trades blocked by new params that were allowed before
    FilteredOutPnL float64 // P&L of filtered trades (positive = avoided losses)
    NewAllowed     int     // trades allowed by new params that were blocked before
    NewAllowedPnL  float64

    ParamsChanged map[string]string // "min_confidence: 70→60"
    Trades        []TradeResult
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
    BlockReason string // if TestAllowed=false
    EntryTime   time.Time
    ExitTime    time.Time
}
```

### 3.6 Report Formatter

```go
// internal/backtest/report.go

func FormatText(result *BacktestResult) string
func FormatJSON(result *BacktestResult) ([]byte, error)
```

**Text format:**
```
=== Backtest: 2026-04-01 — 2026-04-15 ===

Parameters changed:
  min_confidence: 70 → 60
  min_risk_reward_ratio: 1.5 → 1.2

Real results:
  Trades: 29 | Wins: 6 | Losses: 22 | WR: 21% | P&L: -749 ₽
  Max drawdown: -520 ₽ | Avg win: +120 ₽ | Avg loss: -45 ₽

Test results:
  Trades: 35 (+6) | Wins: 9 | Losses: 25 | WR: 26% | P&L: -412 ₽
  Max drawdown: -380 ₽ | Avg win: +115 ₽ | Avg loss: -38 ₽

Impact:
  Filtered out: 3 trades (their P&L: -337 ₽ → avoided losses)
  Newly allowed: 9 trades (their P&L: +674 ₽)

Verdict: Test params BETTER by +337 ₽
```

**JSON format:** same data as `BacktestResult` marshaled to JSON.

### 3.7 Drawdown Calculation

Max drawdown = largest peak-to-trough decline in cumulative P&L:

```go
func calcMaxDrawdown(trades []TradeResult, filterFunc func(TradeResult) bool) float64 {
    var cumPnL, peak, maxDD float64
    for _, t := range trades {
        if !filterFunc(t) { continue }
        cumPnL += t.PnL
        if cumPnL > peak { peak = cumPnL }
        dd := peak - cumPnL
        if dd > maxDD { maxDD = dd }
    }
    return maxDD
}
```

## 4. Error Handling

| Component | Failure | Behavior |
|-----------|---------|----------|
| CycleSnapshot save | JSON marshal / DB error | Log warning, don't affect trading |
| Backtest: no trades | Empty period | Print "No trades found", exit 0 |
| Backtest: empty EntryFeatures | Missing indicator data | Skip indicator-based checks, test only numeric params |
| Backtest: no BUY match for SELL | Orphaned SELL | Skip trade pair |

## 5. Testing Strategy

| Package | Tests |
|---------|-------|
| `backtest/engine_test.go` | Run with mock trades: P&L calculation, win rate, drawdown. Run with override: filtered trade count. Empty period returns zero stats. |
| `backtest/guard_sim_test.go` | ParseFeatures from text (uptrend, ATR, RSI). WouldAllow with confidence filter. WouldAllow with uptrend filter. WouldAllow with combined filters. |
| `backtest/report_test.go` | FormatText contains expected sections. FormatJSON is valid JSON. |

## 6. Files Changed Summary

| File | Change |
|------|--------|
| `internal/scheduler/scheduler.go` | REFACTORED: keep struct + New + Run only |
| `internal/scheduler/cycle.go` | NEW: cycleState, runCycle orchestration |
| `internal/scheduler/data.go` | NEW: data fetching methods (moved from scheduler.go) |
| `internal/scheduler/features.go` | NEW: feature building (moved + reorganized) |
| `internal/scheduler/agents.go` | NEW: AI agent calls + daily review (moved) |
| `internal/scheduler/execution.go` | NEW: guard + executor + trailing stops (moved) |
| `internal/scheduler/reconcile.go` | NEW: orphan reconciliation (moved) |
| `internal/scheduler/snapshots.go` | NEW: log/snapshot saving + cycle snapshot (new) |
| `internal/scheduler/helpers.go` | NEW: utility functions (moved) |
| `internal/backtest/engine.go` | NEW |
| `internal/backtest/guard_sim.go` | NEW |
| `internal/backtest/report.go` | NEW |
| `internal/backtest/engine_test.go` | NEW |
| `internal/backtest/guard_sim_test.go` | NEW |
| `internal/storage/models.go` | MODIFIED: + CycleSnapshot |
| `internal/storage/database.go` | MODIFIED: + AutoMigrate |
| `internal/storage/repository.go` | MODIFIED: + cycle snapshot methods |
| `cmd/backtest/main.go` | NEW |
| `config.example.yaml` | UNCHANGED |
| `README.md` | MODIFIED: + Phase 3 docs |
