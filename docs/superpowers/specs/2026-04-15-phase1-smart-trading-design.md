# Phase 1: Smart Trading Pipeline

**Date:** 2026-04-15
**Status:** Approved
**Scope:** Textual features, dividend calendar, dual AI agents, trade journal with LLM review

## Overview

Phase 1 transforms the trading bot from a single-prompt oracle into a structured pipeline with specialized AI agents, rich contextual data, and a self-improving feedback loop.

### Goals

1. Replace raw OHLCV tables in AI prompts with pre-computed textual features that LLMs understand natively.
2. Add dividend calendar from SmartLab as a signal source.
3. Split the single AI call into Screening Agent (find BUY setups) and Position Manager (manage open positions).
4. Implement Trade Journal with daily LLM review that generates lessons injected into future prompts.

### Non-Goals

- Order book / L2 data (Phase 2)
- Sentiment analysis from Telegram/Pulse (Phase 2)
- Numerical ML models for entry timing (Phase 3)
- Pipeline abstraction / Stage interface refactoring (Phase 3)

## Architecture

### Current Flow (Before)

```
scheduler.runCycle():
  fetch tickers → candles → indicators → screener
  → single AI call (everything: BUY + SELL + HOLD)
  → guard → executor
```

### New Flow (After)

```
scheduler.runCycle():
  1. fetch tickers, candles, portfolio          [unchanged]
  2. fetch dividends (SmartLab, cached 12h)     [NEW]
  3. build textual features                     [NEW: replaces OHLCV tables]
  4. load journal lessons                       [NEW]
  5. IF first cycle of day AND no review for yesterday:
       runDailyReview()                         [NEW]
  6. Screening Agent → BUY candidates           [NEW: replaces single AI call]
  7. Position Manager → HOLD/SELL decisions     [NEW: replaces single AI call]
     (steps 6+7 run in parallel via errgroup)
  8. merge decisions → guard → executor         [unchanged]
```

### New Packages

```
internal/
  dividends/
    smartlab.go           — SmartLab HTML parser + in-memory cache
    smartlab_test.go
  features/
    builder.go            — textual feature builder
    builder_test.go
  journal/
    journal.go            — trade outcome recording, lesson loading, daily review trigger
    journal_test.go
```

### Modified Packages

```
internal/
  ai/
    prompt_screening.go   — NEW: screening agent prompt builder + system prompt
    prompt_position.go    — NEW: position manager prompt builder + system prompt
    prompt_journal.go     — NEW: journal review prompt builder
    deepseek.go           — + ScreeningAnalyze(), PositionAnalyze(), JournalReview()
    types.go              — + ScreeningRequest, PositionRequest, JournalReviewRequest
    prompt.go             — DEPRECATED (kept for rollback)
  scheduler/
    scheduler.go          — dual AI calls (parallel), daily review trigger
  storage/
    models.go             — + TradeOutcome, JournalLesson models
    repository.go         — + journal/outcome query methods
  config/
    config.go             — + DividendsConfig, JournalConfig, AIConfig
  executor/
    executor.go           — save entry_features at BUY time, record outcome at SELL time
```

## Component Details

### 1. Dividend Calendar (`internal/dividends/`)

**Source:** SmartLab (`smart-lab.ru/dividends/index/order_by_yield/desc/`)

**Data structure:**

```go
type DividendInfo struct {
    Ticker               string
    CompanyName          string
    DividendRub          float64   // per share
    YieldPct             float64
    ExDivDate            time.Time // last day to buy for dividend
    RecordDate           time.Time
    PaymentDate          time.Time
    BoardApproved        bool
    ShareholdersApproved bool
    Status               string    // "recommended" | "approved" | "paid"
}
```

**Interface:**

```go
type DividendFetcher struct {
    cache    sync.Map          // ticker → DividendInfo
    lastFetch time.Time
    ttl       time.Duration    // default 12h
    logger    *logger.Logger
}

func NewDividendFetcher(ttl time.Duration, log *logger.Logger) *DividendFetcher
func (f *DividendFetcher) Fetch(ctx context.Context) (map[string]DividendInfo, error)
func (f *DividendFetcher) GetForTicker(ticker string) (DividendInfo, bool)
```

**Caching:** In-memory `sync.Map`, single HTTP fetch per TTL interval. On fetch error, stale cache is used with a warning log. No persistent storage needed — data is ephemeral and re-fetched on restart.

**HTML parsing:** SmartLab dividend page has a `<table>` with columns: ticker, company, div amount, yield, ex-div date, record date, payment date, board recommendation, GOSA status. Parse with Go `html` tokenizer (no external dependency needed). Handle: missing dates (set zero time), missing yield (compute from price if available), Russian date formats ("15.05.2026").

**Filtering:** Only dividends with `ExDivDate` within `lookahead_days` (default 30) are included in AI prompts.

### 2. Textual Features (`internal/features/`)

**Purpose:** Convert numerical indicators and market data into natural language descriptions that LLMs process accurately.

**Interface:**

```go
type TickerFeature struct {
    Ticker      string
    Summary     string   // full textual description for AI prompt
    Components  []string // individual feature lines (for logging)
}

func BuildTickerFeatures(
    snap broker.CandleSnapshot,
    div *dividends.DividendInfo,  // nil if no upcoming dividend
    lessons []journal.Lesson,     // filtered to this ticker
) TickerFeature
```

**Feature computation rules:**

| Feature | Logic | Example output |
|---------|-------|----------------|
| Trend | EMA9 vs EMA21. `gap = (EMA9-EMA21)/EMA21*100` | "uptrend (EMA9 > EMA21, +0.8%)" / "downtrend (-1.2%)" / "flat (0.1%)" |
| Momentum | RSI14 ranges | "RSI 28 — oversold" / "RSI 58 — neutral" / "RSI 82 — overbought" |
| Volume | RelVolume thresholds | "volume elevated (1.8x)" / "volume normal" / "volume low (0.5x)" |
| Volatility | ATR14/price*100 | "ATR 3.2% — sufficient" / "ATR 0.8% — low" |
| Price position | Distance to S/R in ATR units | "0.5 ATR below resistance 308.50" / "at support 298" / "between levels" |
| Streak | Count consecutive up/down days from period data | "rising 3rd day (+4.2%)" / "falling 2nd day (-1.8%)" |
| Dividend | From DividendInfo, days until ex-div | "dividend 8.2% (ex-div in 12 days)" / absent if none |
| Pattern | From OHLCV + indicators | "resistance breakout on volume" / "support bounce" / "range compression" |

**Streak calculation:** Compare period1d, period3d open/close to determine consecutive day direction. Count days with same direction.

**Pattern detection (rule-based, no ML):**

- Breakout: `price >= resistance * 0.995 AND RelVolume > 1.5 AND uptrend` → "resistance breakout on volume"
- Bounce: `price within 1 ATR of support AND RSI < 40` → "support bounce setup"
- Compression: `(resistance - support) / ATR < 2.5` → "range compression (potential breakout)"
- Doji/indecision: `abs(close - open) / (high - low) < 0.15 AND high != low` on period1d → "indecision candle"

**Output format (hybrid narrative + numbers):**

```
SBER (305.20): uptrend 3rd day, volume 1.8x, RSI 58.
Near resistance 308.50 (0.5 ATR). Support 298.
Dividend 8.2% (ex-div in 12 days). ATR 3.2%.
```

**What gets removed from prompts:**

- Raw OHLCV table (`Ticker|Price|Per|Open|High|Low|Vol|Chg%` section)
- Raw indicators table (`Ticker|RSI14|EMA9|EMA21|ATR14|RelVol|Support|Resist` section)

Both replaced by textual features section.

### 3. AI Agents

#### 3.1 Screening Agent

**Purpose:** Find BUY candidates from market data. Does not see portfolio.

**Request type:**

```go
type ScreeningRequest struct {
    TickerFeatures []TickerFeature    // textual features for 15-20 tickers
    Market         MarketContext       // regime, macro changes
    GlobalNews     []string
    TickerNews     map[string][]string // ticker → news titles
    Lessons        []Lesson            // from journal (max 5, high/medium confidence)
    TodayTraded    []string            // anti-churning
    Stats          PerformanceStats    // 7-day aggregate
    CurrentTime    time.Time
    AvailableRub   float64             // so AI knows budget constraint
}
```

**System prompt:**

```
Role: MOEX Screening Analyst (1-3 day horizon).
Task: From the provided tickers, select the best BUY setups.

You do NOT manage positions — focus only on finding new entries.

Evaluate: trend quality, volume confirmation, proximity to levels,
dividend catalyst, news sentiment, setup quality.

Consider lessons from recent trades — avoid repeating known mistakes.

Confidence guide:
  85-95: strong trend + volume + catalyst (news or dividend)
  70-84: good setup with one uncertainty factor
  <70: do not return — will be filtered

Output: JSON array of BUY decisions. Empty [] only if genuinely no ticker
passes basic quality filter.
```

**Output:** `[]AIDecision` with action=BUY only.

**Prompt size:** max 16000 chars. At ~80 chars per ticker feature, fits 20 tickers + news + lessons + stats comfortably.

#### 3.2 Position Manager Agent

**Purpose:** Manage open positions. Does not see market candidates.

**Request type:**

```go
type PositionRequest struct {
    Positions      []PositionContext  // open positions with full context
    Lessons        []Lesson           // from journal (filtered to position tickers)
    CurrentTime    time.Time
    Market         MarketContext
}

type PositionContext struct {
    Ticker          string
    EntryPrice      float64
    CurrentPrice    float64
    PnLPct          float64
    Quantity        int64
    HoldDuration    string    // "2h 15m" / "1d 4h"
    StopLoss        float64
    TakeProfit      float64
    ProgressToTP    float64   // percentage 0-100
    Hypothesis      string    // original BUY reasoning
    Features        string    // current textual features for this ticker
    News            []string  // recent news for this ticker
}
```

**System prompt:**

```
Role: MOEX Position Manager.
Task: For each open position, decide HOLD or SELL.

HOLD is the default. A position needs time to work.
SELL only when:
  (a) Price is near SL and structure is broken (trend reversed, support lost)
  (b) Serious fundamental negative (not just "momentum weakened")
  (c) Position held >2 days with zero progress toward TP
  (d) Original hypothesis is invalidated

Do NOT close profitable trending positions.
Do NOT close for "rotation" or "rebalancing".

For each position, return HOLD or SELL with reasoning.
```

**Output:** `[]AIDecision` with action=HOLD or SELL.

**Prompt size:** max 8000 chars. Typically 2-5 positions, ~200 chars each = comfortable fit.

#### 3.3 Orchestration

Both agents are called **in parallel** via `errgroup.Group`:

```go
g, gCtx := errgroup.WithContext(ctx)

var screeningDecisions, positionDecisions []AIDecision

g.Go(func() error {
    var err error
    screeningDecisions, _, err = s.ai.ScreeningAnalyze(gCtx, screeningReq)
    return err
})

if len(portfolio.Positions) > 0 {
    g.Go(func() error {
        var err error
        positionDecisions, _, err = s.ai.PositionAnalyze(gCtx, positionReq)
        return err
    })
}

// Don't use g.Wait() error directly — collect individual results.
// Each goroutine writes to its own slice; on error, slice stays nil (= no decisions).
_ = g.Wait()
// Merge: nil screening = no BUY; nil position = all HOLD (safe default)
allDecisions := append(positionDecisions, screeningDecisions...)
```

**Failure modes:**

| Scenario | Behavior |
|----------|----------|
| Screening fails, PM succeeds | No BUY this cycle, HOLD/SELL still processed |
| PM fails, Screening succeeds | BUY processed, positions default to HOLD (safe) |
| Both fail | Cycle fails, retry in 30s (existing behavior) |
| Both return `[]` | Valid: no signals found, no position changes |

#### 3.4 DeepSeek Client Changes

```go
// New methods on DeepSeekClient:
func (c *DeepSeekClient) ScreeningAnalyze(ctx, req ScreeningRequest) ([]AIDecision, string, error)
func (c *DeepSeekClient) PositionAnalyze(ctx, req PositionRequest) ([]AIDecision, string, error)
func (c *DeepSeekClient) JournalReview(ctx, req JournalReviewRequest) ([]Lesson, string, error)

// Old method kept for rollback:
func (c *DeepSeekClient) Analyze(ctx, req AnalysisRequest, todayTraded) ([]AIDecision, string, error) // deprecated
```

All three new methods use the same streaming infrastructure as the existing `Analyze()`. The difference is system prompt and user prompt construction.

### 4. Trade Journal (`internal/journal/`)

#### 4.1 Trade Outcome Recording

**When:** At SELL execution time in executor.

**Interface:**

```go
type Journal struct {
    repo   *storage.Repository
    logger *logger.Logger
}

func NewJournal(repo *storage.Repository, log *logger.Logger) *Journal

// RecordOutcome saves a structured outcome for a closed trade.
// Called by executor after successful SELL.
func (j *Journal) RecordOutcome(buyTrade, sellTrade *storage.Trade, entryFeatures string, exitReason string)

// NeedsDailyReview checks if a review for the previous trading day has been done.
func (j *Journal) NeedsDailyReview(loc *time.Location) bool

// LoadLessons returns active lessons (not older than maxAgeDays, confidence >= medium).
func (j *Journal) LoadLessons(maxAgeDays int, maxCount int) ([]Lesson, error)

// LoadLessonsForTickers filters lessons relevant to specific tickers.
func (j *Journal) LoadLessonsForTickers(tickers []string, maxAgeDays int) ([]Lesson, error)
```

**Exit reason classification:**

```go
func classifyExitReason(sellReasoning string) string {
    // Pattern match on reasoning text:
    // "стоп-лосс" / "SL" / "stop" → "sl_hit"
    // "тейк-профит" / "TP" / "take profit" / "фиксируем прибыль" → "tp_reached"
    // "trailing" → "trailing_stop"
    // "orphan" / "reconcile" → "orphan_cleanup"
    // default → "ai_sell"
}
```

**Entry features saving:** When executor processes a BUY, it saves current textual features in a new field on the Trade model (`EntryFeatures text`). This is the snapshot of what the bot "saw" when entering.

#### 4.2 Daily Review

**Trigger:** First cycle of trading day, before main analysis. Scheduler calls `journal.NeedsDailyReview()` — returns true if no `journal_lessons` record exists for previous trading day.

**Review request:**

```go
type JournalReviewRequest struct {
    Outcomes        []TradeOutcome  // closed trades since last review
    Stats           ReviewStats     // aggregate: win rate, avg P&L by setup type, avg hold time
    PreviousLessons []Lesson        // for continuity
    CurrentDate     time.Time
}

type ReviewStats struct {
    TotalTrades     int
    WinRate         float64
    AvgWinPnL       float64
    AvgLossPnL      float64
    AvgHoldHoursWin float64
    AvgHoldHoursLoss float64
    ByExitReason    map[string]int  // "sl_hit": 3, "tp_reached": 1, ...
}
```

**Lesson structure:**

```go
type Lesson struct {
    Pattern        string // what setup/situation
    Observation    string // what the data showed
    Recommendation string // what to do differently
    Confidence     string // "low" | "medium" | "high"
    Tickers        []string // relevant tickers (optional)
}
```

**System prompt:** See Section 3 of design presentation above.

**Storage:** Lessons saved as JSON array in `journal_lessons.lessons_json`. Raw LLM response in `raw_response`. Date indexed by `review_date`.

#### 4.3 Lesson Injection

Lessons are loaded by scheduler and passed to both AI agents:

- **Screening Agent:** gets all lessons (max 5, filtered by age and confidence >= medium)
- **Position Manager:** gets lessons filtered to tickers in current portfolio

**Prompt format:**

```
## Lessons from recent trades
- [high] Resistance breakout on low volume (RelVol < 1.3) was false in 4/5 cases.
  Raise volume threshold for breakout setups.
- [medium] Positions opened before 11:00 MSK have 35% win rate vs 55% after 12:00.
  Prefer entries after 12:00.
```

### 5. Configuration

#### 5.1 New Config Structure

```go
type DividendsConfig struct {
    Enabled       bool          `yaml:"enabled"`
    CacheTTLHours int           `yaml:"cache_ttl_hours"` // default 12
    LookaheadDays int           `yaml:"lookahead_days"`  // default 30
}

type JournalConfig struct {
    Enabled             bool   `yaml:"enabled"`
    MaxLessonsInPrompt  int    `yaml:"max_lessons_in_prompt"`  // default 5
    MaxLessonAgeDays    int    `yaml:"max_lesson_age_days"`    // default 7
    MinTradesForReview  int    `yaml:"min_trades_for_review"`  // default 1
}

type AIConfig struct {
    APIKey         string `yaml:"api_key"`
    Model          string `yaml:"model"`          // default "deepseek-reasoner"
    TimeoutSeconds int    `yaml:"timeout_seconds"` // default 120

    Screening struct {
        MaxChars   int `yaml:"max_chars"`   // default 16000
        MaxTickers int `yaml:"max_tickers"` // default 20
    } `yaml:"screening"`

    PositionManager struct {
        MaxChars int `yaml:"max_chars"` // default 8000
    } `yaml:"position_manager"`

    JournalReview struct {
        MaxChars int    `yaml:"max_chars"` // default 12000
        Model    string `yaml:"model"`     // default "" (uses main model); can be "deepseek-chat" for cheaper review
    } `yaml:"journal_review"`
}
```

#### 5.2 Backward Compatibility

If `ai.*` section is absent in config, fall back to existing `deepseek.*` fields. This allows gradual migration — existing configs continue to work, new features are opt-in.

If `dividends.enabled` is false (or absent), skip dividend fetching entirely. If `journal.enabled` is false, skip outcome recording and daily review.

### 6. Error Handling Summary

| Component | Failure | Behavior |
|-----------|---------|----------|
| SmartLab fetch | HTTP error / parse error | Use stale cache if available. Log warning. Continue without dividends. |
| Feature builder | Missing indicators | Omit that feature line. Always produce at least ticker + price. |
| Screening Agent | Timeout / API error | No BUY this cycle. PM still runs. Log + Telegram alert. |
| Position Manager | Timeout / API error | All positions default to HOLD (safe). Log + Telegram alert. |
| Daily Review | Timeout / API error | Skip review. Retry next cycle. Old lessons remain. |
| Journal record | DB write error | Log error. Non-fatal — trading continues. |
| Both AI agents fail | — | Cycle fails, retry in 30s. Trailing stops still update. |

### 7. Testing Strategy

#### Unit Tests

| Package | Test Cases |
|---------|------------|
| `dividends/` | Parse SmartLab HTML fixture. Cache TTL expiry. Graceful fallback on error. Date parsing (Russian format). Filter by lookahead days. |
| `features/` | Trend detection (up/down/flat). Streak counting (3 up, 2 down, mixed). Level proximity (near support, near resistance, between). Dividend feature with various ex-div distances. Pattern detection (breakout, bounce, compression). Edge cases: zero ATR, no S/R, nil dividend. |
| `journal/` | Outcome creation from BUY+SELL pair. Exit reason classification. Lesson loading with age filter. Lesson loading with ticker filter. NeedsDailyReview logic across weekends. |
| `ai/` | Screening prompt fits in max_chars. Position prompt fits in max_chars. No raw OHLCV tables in any new prompt. Lessons correctly injected. Dividend info included when present. |

#### Integration

One end-to-end test with mock broker data: features → two AI prompts built correctly → decisions merged → guard applied. Uses recorded AI responses (no real API calls).

### 8. Migration Plan

The old single-prompt path (`Analyze()` method, `BuildUserPrompt()` function) is **kept but deprecated**. A config flag could re-enable it for A/B comparison or rollback:

```yaml
ai:
  use_legacy_single_prompt: false  # set true to rollback to old behavior
```

This flag is temporary and will be removed once Phase 1 is validated.

### 9. Files Changed Summary

| File | Change |
|------|--------|
| `internal/dividends/smartlab.go` | NEW |
| `internal/dividends/smartlab_test.go` | NEW |
| `internal/features/builder.go` | NEW |
| `internal/features/builder_test.go` | NEW |
| `internal/journal/journal.go` | NEW |
| `internal/journal/journal_test.go` | NEW |
| `internal/ai/prompt_screening.go` | NEW |
| `internal/ai/prompt_position.go` | NEW |
| `internal/ai/prompt_journal.go` | NEW |
| `internal/ai/deepseek.go` | MODIFIED: + 3 new Analyze methods |
| `internal/ai/types.go` | MODIFIED: + ScreeningRequest, PositionRequest, JournalReviewRequest, Lesson |
| `internal/ai/prompt.go` | DEPRECATED (kept for rollback) |
| `internal/config/config.go` | MODIFIED: + DividendsConfig, JournalConfig, AIConfig |
| `internal/storage/models.go` | MODIFIED: + TradeOutcome, JournalLesson; Trade + EntryFeatures field |
| `internal/storage/repository.go` | MODIFIED: + journal/outcome queries |
| `internal/scheduler/scheduler.go` | MODIFIED: dual AI calls, daily review, feature building |
| `internal/executor/executor.go` | MODIFIED: save entry_features on BUY, record outcome on SELL |
| `config.example.yaml` | MODIFIED: + new config sections |
| `README.md` | MODIFIED: document new features |
