# Phase 1: Smart Trading Pipeline — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Transform the trading bot from a single-prompt oracle into a pipeline with textual features, dividend data, dual AI agents (screening + position management), and a self-improving trade journal.

**Architecture:** Sequential integration into existing scheduler. Three new packages (`dividends`, `features`, `journal`), three new AI prompts replacing the single prompt, parallel AI calls via errgroup. SmartLab HTML parsing for dividends, rule-based feature extraction, daily LLM review for journal lessons.

**Tech Stack:** Go 1.22, DeepSeek R1 API (OpenAI-compatible), SQLite/GORM, `net/html` for HTML parsing, `golang.org/x/sync/errgroup` for parallel AI calls.

**Spec:** `docs/superpowers/specs/2026-04-15-phase1-smart-trading-design.md`

---

## Task 1: Configuration — New Config Structs

**Files:**
- Modify: `internal/config/config.go`
- Modify: `config.example.yaml`

- [ ] **Step 1: Add new config types to `internal/config/config.go`**

Add after the existing `LoggingConfig` struct (after line ~78):

```go
type DividendsConfig struct {
	Enabled       bool `yaml:"enabled"`
	CacheTTLHours int  `yaml:"cache_ttl_hours"`
	LookaheadDays int  `yaml:"lookahead_days"`
}

type JournalConfig struct {
	Enabled            bool `yaml:"enabled"`
	MaxLessonsInPrompt int  `yaml:"max_lessons_in_prompt"`
	MaxLessonAgeDays   int  `yaml:"max_lesson_age_days"`
	MinTradesForReview int  `yaml:"min_trades_for_review"`
}

type AIAgentsConfig struct {
	Screening struct {
		MaxChars   int `yaml:"max_chars"`
		MaxTickers int `yaml:"max_tickers"`
	} `yaml:"screening"`
	PositionManager struct {
		MaxChars int `yaml:"max_chars"`
	} `yaml:"position_manager"`
	JournalReview struct {
		MaxChars int    `yaml:"max_chars"`
		Model    string `yaml:"model"`
	} `yaml:"journal_review"`
}
```

Add fields to the main `Config` struct:

```go
type Config struct {
	Tinkoff   TinkoffConfig   `yaml:"tinkoff"`
	DeepSeek  DeepSeekConfig  `yaml:"deepseek"`
	Trading   TradingConfig   `yaml:"trading"`
	Telegram  TelegramConfig  `yaml:"telegram"`
	Web       WebConfig       `yaml:"web"`
	Logging   LoggingConfig   `yaml:"logging"`
	Dividends DividendsConfig `yaml:"dividends"`
	Journal   JournalConfig   `yaml:"journal"`
	AIAgents  AIAgentsConfig  `yaml:"ai_agents"`
}
```

- [ ] **Step 2: Add defaults in `setDefaults()` function**

Add at the end of `setDefaults()` (before the closing `}`):

```go
	if cfg.Dividends.CacheTTLHours == 0 {
		cfg.Dividends.CacheTTLHours = 12
	}
	if cfg.Dividends.LookaheadDays == 0 {
		cfg.Dividends.LookaheadDays = 30
	}
	if cfg.Journal.MaxLessonsInPrompt == 0 {
		cfg.Journal.MaxLessonsInPrompt = 5
	}
	if cfg.Journal.MaxLessonAgeDays == 0 {
		cfg.Journal.MaxLessonAgeDays = 7
	}
	if cfg.Journal.MinTradesForReview == 0 {
		cfg.Journal.MinTradesForReview = 1
	}
	if cfg.AIAgents.Screening.MaxChars == 0 {
		cfg.AIAgents.Screening.MaxChars = 16000
	}
	if cfg.AIAgents.Screening.MaxTickers == 0 {
		cfg.AIAgents.Screening.MaxTickers = 20
	}
	if cfg.AIAgents.PositionManager.MaxChars == 0 {
		cfg.AIAgents.PositionManager.MaxChars = 8000
	}
	if cfg.AIAgents.JournalReview.MaxChars == 0 {
		cfg.AIAgents.JournalReview.MaxChars = 12000
	}
```

- [ ] **Step 3: Update `config.example.yaml`**

Add new sections at the end (before logging):

```yaml
# Dividend calendar (SmartLab)
dividends:
  enabled: true
  cache_ttl_hours: 12
  lookahead_days: 30

# Trade Journal with daily LLM review
journal:
  enabled: true
  max_lessons_in_prompt: 5
  max_lesson_age_days: 7
  min_trades_for_review: 1

# AI agent-specific settings (override deepseek.* per agent)
ai_agents:
  screening:
    max_chars: 16000
    max_tickers: 20
  position_manager:
    max_chars: 8000
  journal_review:
    max_chars: 12000
    # Use cheaper model for review (empty = use main model)
    model: ""
```

- [ ] **Step 4: Build and verify**

Run: `CGO_ENABLED=1 go build ./...`
Expected: clean build, no errors.

- [ ] **Step 5: Commit**

```bash
git add internal/config/config.go config.example.yaml
git commit -m "feat: add config for dividends, journal, and AI agents"
```

---

## Task 2: Storage — New Models and Repository Methods

**Files:**
- Modify: `internal/storage/models.go`
- Modify: `internal/storage/database.go`
- Modify: `internal/storage/repository.go`

- [ ] **Step 1: Add new models to `internal/storage/models.go`**

Add `EntryFeatures` field to existing `Trade` struct (after `Reasoning` field):

```go
type Trade struct {
	ID                uint      `gorm:"primarykey" json:"id"`
	CreatedAt         time.Time `json:"created_at"`
	UpdatedAt         time.Time `json:"updated_at"`
	Ticker            string    `gorm:"not null" json:"ticker" gorm:"index"`
	Action            string    `gorm:"not null" json:"action"`
	Price             float64   `gorm:"not null" json:"price"`
	Quantity          int64     `gorm:"not null" json:"quantity"`
	OrderID           string    `json:"order_id"`
	StopLossPrice     float64   `json:"stop_loss_price"`
	TakeProfitPrice   float64   `json:"take_profit_price"`
	StopLossOrderID   string    `json:"stop_loss_order_id"`
	TakeProfitOrderID string    `json:"take_profit_order_id"`
	PnL               float64   `json:"pnl"`
	Reasoning         string    `json:"reasoning"`
	EntryFeatures     string    `gorm:"type:text" json:"entry_features"`
	Status            string    `gorm:"not null;default:open" json:"status"`
}
```

Add new models at end of file:

```go
type TradeOutcome struct {
	ID                uint      `gorm:"primarykey" json:"id"`
	CreatedAt         time.Time `json:"created_at"`
	TradeID           uint      `gorm:"not null" json:"trade_id"`
	Hypothesis        string    `gorm:"type:text" json:"hypothesis"`
	EntryFeatures     string    `gorm:"type:text" json:"entry_features"`
	ExitReason        string    `json:"exit_reason"`
	Outcome           string    `json:"outcome"`
	PnL               float64   `json:"pnl"`
	HoldDurationHours float64   `json:"hold_duration_hours"`
	WhatHappened      string    `gorm:"type:text" json:"what_happened"`
}

type JournalLesson struct {
	ID             uint      `gorm:"primarykey" json:"id"`
	CreatedAt      time.Time `json:"created_at"`
	ReviewDate     string    `gorm:"not null;index" json:"review_date"`
	LessonsJSON    string    `gorm:"type:text" json:"lessons_json"`
	RawResponse    string    `gorm:"type:text" json:"raw_response"`
	TradesReviewed int       `json:"trades_reviewed"`
}
```

- [ ] **Step 2: Register new models in `internal/storage/database.go`**

Change the `AutoMigrate` line to include new models:

```go
if err := db.AutoMigrate(&Trade{}, &AnalysisLog{}, &PortfolioSnapshot{}, &TradeOutcome{}, &JournalLesson{}); err != nil {
```

- [ ] **Step 3: Add repository methods to `internal/storage/repository.go`**

Add at end of file:

```go
// Trade Outcomes

func (r *Repository) SaveTradeOutcome(outcome *TradeOutcome) error {
	return r.db.Create(outcome).Error
}

func (r *Repository) GetOutcomesSinceLastReview() ([]TradeOutcome, error) {
	// Find last review date
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

// GetBuyTradeForSell finds the matching BUY trade for a SELL by ticker.
func (r *Repository) GetBuyTradeForSell(ticker string) (*Trade, error) {
	var trade Trade
	err := r.db.Where("ticker = ? AND action = ? AND status = ?", ticker, "BUY", "closed").
		Order("updated_at DESC").First(&trade).Error
	if err != nil {
		return nil, err
	}
	return &trade, nil
}
```

- [ ] **Step 4: Build and verify**

Run: `CGO_ENABLED=1 go build ./...`
Expected: clean build.

- [ ] **Step 5: Commit**

```bash
git add internal/storage/models.go internal/storage/database.go internal/storage/repository.go
git commit -m "feat: add TradeOutcome, JournalLesson models and repository methods"
```

---

## Task 3: Dividend Calendar — SmartLab Parser

**Files:**
- Create: `internal/dividends/smartlab.go`
- Create: `internal/dividends/smartlab_test.go`
- Create: `internal/dividends/testdata/smartlab.html` (test fixture)

- [ ] **Step 1: Create test fixture**

Create `internal/dividends/testdata/smartlab.html` — a minimal HTML fixture mimicking SmartLab's dividend table structure. Fetch a real sample first:

Run: `mkdir -p internal/dividends/testdata && curl -sL 'https://smart-lab.ru/dividends/index/order_by_yield/desc/' | head -500 > /tmp/smartlab_sample.html && head -50 /tmp/smartlab_sample.html`

Inspect the HTML structure (table class, column order) and create a minimal fixture with 3-4 rows. The fixture should contain a `<table>` with dividend data rows. Store at `internal/dividends/testdata/smartlab.html`.

- [ ] **Step 2: Write tests in `internal/dividends/smartlab_test.go`**

```go
package dividends

import (
	"context"
	"os"
	"testing"
	"time"
)

func TestParseDividendHTML(t *testing.T) {
	data, err := os.ReadFile("testdata/smartlab.html")
	if err != nil {
		t.Skipf("no test fixture: %v", err)
	}

	divs, err := parseDividendHTML(data)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	if len(divs) == 0 {
		t.Fatal("expected at least 1 dividend, got 0")
	}

	// Verify structure
	for _, d := range divs {
		if d.Ticker == "" {
			t.Error("empty ticker")
		}
		if d.YieldPct < 0 || d.YieldPct > 100 {
			t.Errorf("invalid yield for %s: %.2f", d.Ticker, d.YieldPct)
		}
	}
}

func TestParseRussianDate(t *testing.T) {
	tests := []struct {
		input string
		want  time.Time
		ok    bool
	}{
		{"15.05.2026", time.Date(2026, 5, 15, 0, 0, 0, 0, time.UTC), true},
		{"01.12.2025", time.Date(2025, 12, 1, 0, 0, 0, 0, time.UTC), true},
		{"", time.Time{}, false},
		{"invalid", time.Time{}, false},
	}
	for _, tt := range tests {
		got, err := parseDate(tt.input)
		if tt.ok && err != nil {
			t.Errorf("parseDate(%q) error: %v", tt.input, err)
		}
		if !tt.ok && err == nil {
			t.Errorf("parseDate(%q) expected error", tt.input)
		}
		if tt.ok && !got.Equal(tt.want) {
			t.Errorf("parseDate(%q) = %v, want %v", tt.input, got, tt.want)
		}
	}
}

func TestFilterByLookahead(t *testing.T) {
	now := time.Now()
	divs := map[string]DividendInfo{
		"NEAR": {Ticker: "NEAR", ExDivDate: now.Add(10 * 24 * time.Hour)},
		"FAR":  {Ticker: "FAR", ExDivDate: now.Add(60 * 24 * time.Hour)},
		"PAST": {Ticker: "PAST", ExDivDate: now.Add(-5 * 24 * time.Hour)},
	}
	filtered := FilterByLookahead(divs, 30)
	if _, ok := filtered["NEAR"]; !ok {
		t.Error("NEAR should be included (10 days)")
	}
	if _, ok := filtered["FAR"]; ok {
		t.Error("FAR should be excluded (60 days > 30)")
	}
	if _, ok := filtered["PAST"]; ok {
		t.Error("PAST should be excluded (in the past)")
	}
}

func TestCacheTTL(t *testing.T) {
	f := NewFetcher(1*time.Millisecond, nil) // 1ms TTL for test
	f.cache.Store("TEST", DividendInfo{Ticker: "TEST"})
	f.lastFetch = time.Now()

	// Should use cache
	if _, ok := f.GetForTicker("TEST"); !ok {
		t.Error("expected cache hit")
	}

	time.Sleep(5 * time.Millisecond)
	// TTL expired — GetForTicker still returns from map (cache not cleared until next Fetch)
	// But isCacheValid() should return false
	if f.isCacheValid() {
		t.Error("cache should be expired")
	}
}
```

- [ ] **Step 3: Run tests to verify they fail**

Run: `CGO_ENABLED=1 go test ./internal/dividends/ -v 2>&1 | head -30`
Expected: compilation error (package doesn't exist yet).

- [ ] **Step 4: Implement `internal/dividends/smartlab.go`**

```go
package dividends

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/html"

	"github.com/camuig/rus-trader/internal/logger"
)

type DividendInfo struct {
	Ticker               string
	CompanyName          string
	DividendRub          float64
	YieldPct             float64
	ExDivDate            time.Time
	RecordDate           time.Time
	Status               string // "recommended" | "approved" | "paid" | "forecast"
}

type Fetcher struct {
	cache     sync.Map
	lastFetch time.Time
	ttl       time.Duration
	logger    *logger.Logger
	mu        sync.Mutex
}

const smartLabURL = "https://smart-lab.ru/dividends/index/order_by_yield/desc/"

func NewFetcher(ttl time.Duration, log *logger.Logger) *Fetcher {
	return &Fetcher{ttl: ttl, logger: log}
}

func (f *Fetcher) isCacheValid() bool {
	return !f.lastFetch.IsZero() && time.Since(f.lastFetch) < f.ttl
}

func (f *Fetcher) Fetch(ctx context.Context) (map[string]DividendInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.isCacheValid() {
		return f.cacheToMap(), nil
	}

	req, err := http.NewRequestWithContext(ctx, "GET", smartLabURL, nil)
	if err != nil {
		return f.cacheToMap(), fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 rus-trader/1.0")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		if f.logger != nil {
			f.logger.Error("smartlab fetch failed, using stale cache", "error", err)
		}
		return f.cacheToMap(), nil // graceful: return stale cache
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return f.cacheToMap(), nil
	}

	divs, err := parseDividendHTML(body)
	if err != nil {
		if f.logger != nil {
			f.logger.Error("smartlab parse failed", "error", err)
		}
		return f.cacheToMap(), nil
	}

	// Update cache
	newCache := &sync.Map{}
	for _, d := range divs {
		newCache.Store(d.Ticker, d)
	}
	f.cache = *newCache
	f.lastFetch = time.Now()

	if f.logger != nil {
		f.logger.Info("dividends fetched from SmartLab", "count", len(divs))
	}

	return f.cacheToMap(), nil
}

func (f *Fetcher) GetForTicker(ticker string) (DividendInfo, bool) {
	val, ok := f.cache.Load(ticker)
	if !ok {
		return DividendInfo{}, false
	}
	return val.(DividendInfo), true
}

func (f *Fetcher) cacheToMap() map[string]DividendInfo {
	result := make(map[string]DividendInfo)
	f.cache.Range(func(key, value any) bool {
		result[key.(string)] = value.(DividendInfo)
		return true
	})
	return result
}

// FilterByLookahead returns only dividends with ExDivDate within lookaheadDays from now.
func FilterByLookahead(divs map[string]DividendInfo, lookaheadDays int) map[string]DividendInfo {
	now := time.Now()
	cutoff := now.Add(time.Duration(lookaheadDays) * 24 * time.Hour)
	result := make(map[string]DividendInfo)
	for k, d := range divs {
		if d.ExDivDate.After(now) && d.ExDivDate.Before(cutoff) {
			result[k] = d
		}
	}
	return result
}

// parseDividendHTML parses SmartLab dividend table HTML.
func parseDividendHTML(data []byte) ([]DividendInfo, error) {
	doc, err := html.Parse(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("parse html: %w", err)
	}

	var divs []DividendInfo
	var inTable, inBody bool
	var currentRow []string

	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode {
			switch n.Data {
			case "table":
				for _, a := range n.Attr {
					if a.Key == "class" && strings.Contains(a.Val, "dividend") {
						inTable = true
					}
				}
			case "tbody":
				if inTable {
					inBody = true
				}
			case "tr":
				if inBody {
					currentRow = nil
				}
			case "td":
				if inBody {
					currentRow = append(currentRow, extractText(n))
				}
			}
		}

		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}

		// Post-visit: end of row
		if n.Type == html.ElementNode && n.Data == "tr" && inBody && len(currentRow) >= 5 {
			if d, ok := parseRow(currentRow); ok {
				divs = append(divs, d)
			}
		}
		if n.Type == html.ElementNode && n.Data == "table" && inTable {
			inTable = false
			inBody = false
		}
	}
	walk(doc)

	// If structured table parsing found nothing, try a simpler approach:
	// look for all <tr> elements and parse cells.
	if len(divs) == 0 {
		divs = parseTableFallback(doc)
	}

	return divs, nil
}

func parseTableFallback(doc *html.Node) []DividendInfo {
	var divs []DividendInfo
	var rows [][]string

	var walk func(*html.Node)
	var inRow bool
	var currentRow []string

	walk = func(n *html.Node) {
		if n.Type == html.ElementNode {
			if n.Data == "tr" {
				inRow = true
				currentRow = nil
			}
			if n.Data == "td" && inRow {
				currentRow = append(currentRow, extractText(n))
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
		if n.Type == html.ElementNode && n.Data == "tr" && inRow {
			if len(currentRow) >= 5 {
				rows = append(rows, currentRow)
			}
			inRow = false
		}
	}
	walk(doc)

	for _, row := range rows {
		if d, ok := parseRow(row); ok {
			divs = append(divs, d)
		}
	}
	return divs
}

func parseRow(cells []string) (DividendInfo, bool) {
	// SmartLab columns vary; look for a cell that looks like a ticker (uppercase 2-5 chars)
	// and cells with dates, percentages, etc.
	var d DividendInfo
	for _, cell := range cells {
		cell = strings.TrimSpace(cell)
		if cell == "" {
			continue
		}
		// Ticker: uppercase letters, 2-6 chars
		if len(cell) >= 2 && len(cell) <= 6 && cell == strings.ToUpper(cell) && isAlpha(cell) {
			if d.Ticker == "" {
				d.Ticker = cell
			}
		}
		// Yield: contains %
		if strings.Contains(cell, "%") && d.YieldPct == 0 {
			pct := strings.ReplaceAll(cell, "%", "")
			pct = strings.ReplaceAll(pct, ",", ".")
			pct = strings.TrimSpace(pct)
			if v, err := strconv.ParseFloat(pct, 64); err == nil && v > 0 && v < 100 {
				d.YieldPct = v
			}
		}
		// Date: dd.mm.yyyy
		if t, err := parseDate(cell); err == nil {
			if d.ExDivDate.IsZero() {
				d.ExDivDate = t
			} else if d.RecordDate.IsZero() {
				d.RecordDate = t
			}
		}
		// Dividend amount: number with decimal
		if d.DividendRub == 0 {
			clean := strings.ReplaceAll(cell, ",", ".")
			clean = strings.TrimSpace(clean)
			if v, err := strconv.ParseFloat(clean, 64); err == nil && v > 0 && v < 100000 {
				// Likely a dividend per share, not a year or percentage
				if !strings.Contains(cell, "%") && !strings.Contains(cell, ".2026") && !strings.Contains(cell, ".2025") {
					d.DividendRub = v
				}
			}
		}
	}
	if d.Ticker == "" {
		return d, false
	}
	return d, true
}

func extractText(n *html.Node) string {
	var sb strings.Builder
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.TextNode {
			sb.WriteString(n.Data)
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return strings.TrimSpace(sb.String())
}

func parseDate(s string) (time.Time, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}, fmt.Errorf("empty date")
	}
	t, err := time.Parse("02.01.2006", s)
	if err != nil {
		return time.Time{}, err
	}
	return t, nil
}

func isAlpha(s string) bool {
	for _, r := range s {
		if (r < 'A' || r > 'Z') && (r < 'a' || r > 'z') {
			return false
		}
	}
	return true
}
```

- [ ] **Step 5: Add `golang.org/x/net` dependency**

Run: `go get golang.org/x/net/html`

- [ ] **Step 6: Create test fixture from real SmartLab page**

Run the following to fetch and trim a real fixture. Then manually inspect and clean it to a minimal HTML containing the dividend table with 3-5 rows:

```bash
curl -sL 'https://smart-lab.ru/dividends/index/order_by_yield/desc/' -o /tmp/smartlab_full.html 2>/dev/null
# Extract just the table portion for the fixture — do this manually or:
# Create a minimal fixture with known data for deterministic tests
```

If SmartLab is inaccessible, create a synthetic fixture with known data.

- [ ] **Step 7: Run tests**

Run: `CGO_ENABLED=1 go test ./internal/dividends/ -v`
Expected: all tests pass (fixture test may skip if no fixture file).

- [ ] **Step 8: Commit**

```bash
git add internal/dividends/ go.mod go.sum
git commit -m "feat: add SmartLab dividend calendar parser with caching"
```

---

## Task 4: Textual Feature Builder

**Files:**
- Create: `internal/features/builder.go`
- Create: `internal/features/builder_test.go`

- [ ] **Step 1: Write tests in `internal/features/builder_test.go`**

```go
package features

import (
	"testing"
	"time"

	"github.com/camuig/rus-trader/internal/broker"
	"github.com/camuig/rus-trader/internal/dividends"
	"github.com/camuig/rus-trader/internal/indicators"
)

func snap(ticker string, price float64, ind indicators.Indicators) broker.CandleSnapshot {
	return broker.CandleSnapshot{
		Ticker:    ticker,
		LastPrice: price,
		Period1d:  broker.PeriodOHLCV{Open: price * 0.99, High: price * 1.01, Low: price * 0.98, Close: price, Volume: 1000000},
		Period3d:  broker.PeriodOHLCV{Open: price * 0.97, High: price * 1.02, Low: price * 0.96, Close: price, Volume: 3000000},
		Period1w:  broker.PeriodOHLCV{Open: price * 0.95, High: price * 1.03, Low: price * 0.94, Close: price, Volume: 7000000},
		Indicators: ind,
	}
}

func TestBuildFeatures_Uptrend(t *testing.T) {
	s := snap("SBER", 305, indicators.Indicators{
		RSI14: 58, EMA9: 306, EMA21: 303, ATR14: 4.5, RelVolume: 1.8,
		Support: 298, Resistance: 312,
	})
	f := BuildTickerFeatures(s, nil)
	if f.Ticker != "SBER" {
		t.Errorf("ticker = %q, want SBER", f.Ticker)
	}
	if !containsAny(f.Summary, "uptrend", "аптренд") {
		t.Errorf("expected uptrend mention in %q", f.Summary)
	}
	if !containsAny(f.Summary, "1.8x", "1.8") {
		t.Errorf("expected volume mention in %q", f.Summary)
	}
}

func TestBuildFeatures_Downtrend(t *testing.T) {
	s := snap("GAZP", 150, indicators.Indicators{
		RSI14: 35, EMA9: 148, EMA21: 153, ATR14: 3.0, RelVolume: 0.8,
	})
	f := BuildTickerFeatures(s, nil)
	if !containsAny(f.Summary, "downtrend", "даунтренд") {
		t.Errorf("expected downtrend in %q", f.Summary)
	}
}

func TestBuildFeatures_WithDividend(t *testing.T) {
	s := snap("SBER", 305, indicators.Indicators{
		RSI14: 58, EMA9: 306, EMA21: 303, ATR14: 4.5, RelVolume: 1.0,
	})
	div := &dividends.DividendInfo{
		Ticker:    "SBER",
		YieldPct:  8.2,
		ExDivDate: time.Now().Add(12 * 24 * time.Hour),
	}
	f := BuildTickerFeatures(s, div)
	if !containsAny(f.Summary, "8.2%", "дивиденд", "dividend") {
		t.Errorf("expected dividend mention in %q", f.Summary)
	}
}

func TestBuildFeatures_NearResistance(t *testing.T) {
	s := snap("TEST", 100, indicators.Indicators{
		RSI14: 55, EMA9: 101, EMA21: 99, ATR14: 2.0, RelVolume: 1.5,
		Support: 95, Resistance: 101,
	})
	f := BuildTickerFeatures(s, nil)
	if !containsAny(f.Summary, "resist", "сопротивл") {
		t.Errorf("expected resistance mention in %q", f.Summary)
	}
}

func TestBuildFeatures_Oversold(t *testing.T) {
	s := snap("TEST", 50, indicators.Indicators{
		RSI14: 25, EMA9: 49, EMA21: 52, ATR14: 1.0, RelVolume: 2.0,
	})
	f := BuildTickerFeatures(s, nil)
	if !containsAny(f.Summary, "oversold", "перепродан") {
		t.Errorf("expected oversold in %q", f.Summary)
	}
}

func TestBuildFeatures_Streak(t *testing.T) {
	s := broker.CandleSnapshot{
		Ticker:    "TEST",
		LastPrice: 100,
		Period1d:  broker.PeriodOHLCV{Open: 98, Close: 100},
		Period3d:  broker.PeriodOHLCV{Open: 95, Close: 100},
		Period1w:  broker.PeriodOHLCV{Open: 90, Close: 100},
		Indicators: indicators.Indicators{
			RSI14: 60, EMA9: 101, EMA21: 99, ATR14: 2.0, RelVolume: 1.0,
		},
	}
	f := BuildTickerFeatures(s, nil)
	// All periods are up, so should mention rising streak
	if f.Summary == "" {
		t.Error("expected non-empty summary")
	}
}

func containsAny(s string, substrs ...string) bool {
	lower := strings.ToLower(s)
	for _, sub := range substrs {
		if strings.Contains(lower, strings.ToLower(sub)) {
			return true
		}
	}
	return false
}
```

Add the missing import for strings:
```go
import (
	"strings"
	"testing"
	"time"
	// ...
)
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `CGO_ENABLED=1 go test ./internal/features/ -v 2>&1 | head -10`
Expected: compilation error.

- [ ] **Step 3: Implement `internal/features/builder.go`**

```go
package features

import (
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/camuig/rus-trader/internal/broker"
	"github.com/camuig/rus-trader/internal/dividends"
	"github.com/camuig/rus-trader/internal/indicators"
)

// TickerFeature is the textual representation of a ticker for AI prompts.
type TickerFeature struct {
	Ticker  string
	Summary string // full multi-line description
}

// BuildTickerFeatures converts numerical snapshot + optional dividend into a text description.
func BuildTickerFeatures(snap broker.CandleSnapshot, div *dividends.DividendInfo) TickerFeature {
	ind := snap.Indicators
	price := snap.LastPrice
	var parts []string

	// Trend
	parts = append(parts, trendText(ind))

	// Streak
	if s := streakText(snap); s != "" {
		parts = append(parts, s)
	}

	// Volume
	parts = append(parts, volumeText(ind.RelVolume))

	// Momentum (RSI)
	parts = append(parts, rsiText(ind.RSI14))

	// Price position relative to S/R
	if s := levelText(price, ind); s != "" {
		parts = append(parts, s)
	}

	// Pattern
	if s := patternText(snap); s != "" {
		parts = append(parts, s)
	}

	// Volatility
	if price > 0 && ind.ATR14 > 0 {
		atrPct := ind.ATR14 / price * 100
		parts = append(parts, fmt.Sprintf("ATR %.1f%%", atrPct))
	}

	// Dividend
	if div != nil && !div.ExDivDate.IsZero() && div.ExDivDate.After(time.Now()) {
		daysUntil := int(time.Until(div.ExDivDate).Hours() / 24)
		if daysUntil >= 0 {
			parts = append(parts, fmt.Sprintf("дивиденд %.1f%% (отсечка через %d дн)", div.YieldPct, daysUntil))
		}
	}

	header := fmt.Sprintf("%s (%.2f)", snap.Ticker, price)
	summary := header + ": " + strings.Join(parts, ", ") + "."

	return TickerFeature{
		Ticker:  snap.Ticker,
		Summary: summary,
	}
}

func trendText(ind indicators.Indicators) string {
	if ind.EMA9 <= 0 || ind.EMA21 <= 0 {
		return "тренд неизвестен"
	}
	gap := (ind.EMA9 - ind.EMA21) / ind.EMA21 * 100
	switch {
	case gap > 0.3:
		return fmt.Sprintf("аптренд (EMA +%.1f%%)", gap)
	case gap < -0.3:
		return fmt.Sprintf("даунтренд (EMA %.1f%%)", gap)
	default:
		return "флэт"
	}
}

func streakText(snap broker.CandleSnapshot) string {
	up1d := snap.Period1d.Close > snap.Period1d.Open && snap.Period1d.Open > 0
	up3d := snap.Period3d.Close > snap.Period3d.Open && snap.Period3d.Open > 0

	down1d := snap.Period1d.Close < snap.Period1d.Open && snap.Period1d.Open > 0
	down3d := snap.Period3d.Close < snap.Period3d.Open && snap.Period3d.Open > 0

	if up1d && up3d {
		chg := 0.0
		if snap.Period3d.Open > 0 {
			chg = (snap.Period3d.Close - snap.Period3d.Open) / snap.Period3d.Open * 100
		}
		return fmt.Sprintf("рост 3+ дней (%+.1f%%)", chg)
	}
	if down1d && down3d {
		chg := 0.0
		if snap.Period3d.Open > 0 {
			chg = (snap.Period3d.Close - snap.Period3d.Open) / snap.Period3d.Open * 100
		}
		return fmt.Sprintf("падение 3+ дней (%.1f%%)", chg)
	}
	return ""
}

func volumeText(relVol float64) string {
	switch {
	case relVol >= 2.0:
		return fmt.Sprintf("объём высокий (%.1fx)", relVol)
	case relVol >= 1.5:
		return fmt.Sprintf("объём повышен (%.1fx)", relVol)
	case relVol >= 1.0:
		return "объём обычный"
	case relVol > 0:
		return fmt.Sprintf("объём низкий (%.1fx)", relVol)
	default:
		return "объём неизвестен"
	}
}

func rsiText(rsi float64) string {
	switch {
	case rsi <= 0:
		return "RSI нет данных"
	case rsi < 30:
		return fmt.Sprintf("RSI %.0f — перепродан", rsi)
	case rsi < 40:
		return fmt.Sprintf("RSI %.0f — низкий", rsi)
	case rsi <= 60:
		return fmt.Sprintf("RSI %.0f — нейтральный", rsi)
	case rsi <= 70:
		return fmt.Sprintf("RSI %.0f — повышен", rsi)
	case rsi <= 80:
		return fmt.Sprintf("RSI %.0f — высокий", rsi)
	default:
		return fmt.Sprintf("RSI %.0f — перекуплен", rsi)
	}
}

func levelText(price float64, ind indicators.Indicators) string {
	if price <= 0 || ind.ATR14 <= 0 {
		return ""
	}
	var parts []string

	if ind.Resistance > 0 {
		distATR := (ind.Resistance - price) / ind.ATR14
		if distATR >= 0 && distATR <= 1.5 {
			parts = append(parts, fmt.Sprintf("у сопротивления %.2f (%.1f ATR)", ind.Resistance, distATR))
		}
	}
	if ind.Support > 0 {
		distATR := (price - ind.Support) / ind.ATR14
		if distATR >= 0 && distATR <= 1.5 {
			parts = append(parts, fmt.Sprintf("у поддержки %.2f (%.1f ATR)", ind.Support, distATR))
		}
	}
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, ", ")
}

func patternText(snap broker.CandleSnapshot) string {
	ind := snap.Indicators
	price := snap.LastPrice
	if price <= 0 || ind.ATR14 <= 0 {
		return ""
	}

	uptrend := ind.EMA9 > 0 && ind.EMA21 > 0 && ind.EMA9 > ind.EMA21

	// Breakout above resistance on volume
	if ind.Resistance > 0 && price >= ind.Resistance*0.995 && ind.RelVolume > 1.5 && uptrend {
		return "пробой сопротивления на объёме"
	}

	// Bounce from support
	if ind.Support > 0 && ind.ATR14 > 0 {
		distATR := (price - ind.Support) / ind.ATR14
		if distATR >= 0 && distATR <= 1.0 && ind.RSI14 < 40 {
			return "отскок от поддержки"
		}
	}

	// Range compression
	if ind.Support > 0 && ind.Resistance > 0 {
		rangeATR := (ind.Resistance - ind.Support) / ind.ATR14
		if rangeATR > 0 && rangeATR < 2.5 {
			return "сжатие диапазона"
		}
	}

	// Doji / indecision on daily
	if snap.Period1d.High > snap.Period1d.Low && snap.Period1d.Low > 0 {
		body := math.Abs(snap.Period1d.Close - snap.Period1d.Open)
		wick := snap.Period1d.High - snap.Period1d.Low
		if wick > 0 && body/wick < 0.15 {
			return "свеча неопределённости"
		}
	}

	return ""
}
```

- [ ] **Step 4: Run tests**

Run: `CGO_ENABLED=1 go test ./internal/features/ -v`
Expected: all tests pass.

- [ ] **Step 5: Commit**

```bash
git add internal/features/
git commit -m "feat: add textual feature builder for AI prompts"
```

---

## Task 5: Trade Journal — Outcome Recording and Lesson Loading

**Files:**
- Create: `internal/journal/journal.go`
- Create: `internal/journal/journal_test.go`

- [ ] **Step 1: Write tests in `internal/journal/journal_test.go`**

```go
package journal

import (
	"testing"
	"time"
)

func TestClassifyExitReason(t *testing.T) {
	tests := []struct {
		reasoning string
		want      string
	}{
		{"Цена пробила стоп-лосс", "sl_hit"},
		{"SL triggered at 250.00", "sl_hit"},
		{"Take profit достигнут", "tp_reached"},
		{"Фиксируем прибыль", "tp_reached"},
		{"trailing stop сработал", "trailing_stop"},
		{"orphaned position cleanup", "orphan_cleanup"},
		{"auto-reconcile: позиция не найдена", "orphan_cleanup"},
		{"Нисходящий тренд, закрываю позицию", "ai_sell"},
		{"", "ai_sell"},
	}
	for _, tt := range tests {
		got := ClassifyExitReason(tt.reasoning)
		if got != tt.want {
			t.Errorf("ClassifyExitReason(%q) = %q, want %q", tt.reasoning, got, tt.want)
		}
	}
}

func TestComputeWhatHappened(t *testing.T) {
	got := ComputeWhatHappened(100.0, 97.0, 3.5)
	if got == "" {
		t.Error("expected non-empty what_happened")
	}
	// Should mention the price drop
	if !containsAny(got, "упала", "fell", "-3.0%", "-3.00%") {
		t.Errorf("expected price drop mention in %q", got)
	}
}

func TestComputeWhatHappened_Profit(t *testing.T) {
	got := ComputeWhatHappened(100.0, 105.0, 8.0)
	if !containsAny(got, "выросла", "rose", "+5.0%", "+5.00%") {
		t.Errorf("expected price rise mention in %q", got)
	}
}

func TestNeedsDailyReview(t *testing.T) {
	// Monday morning, no review for Friday
	loc, _ := time.LoadLocation("Europe/Moscow")
	// This is a logic test — just verify PreviousTradingDay
	fri := PreviousTradingDay(
		time.Date(2026, 4, 13, 10, 0, 0, 0, loc), // Monday
	)
	if fri.Weekday() != time.Friday {
		t.Errorf("previous trading day from Monday should be Friday, got %v", fri.Weekday())
	}

	sat := PreviousTradingDay(
		time.Date(2026, 4, 11, 10, 0, 0, 0, loc), // Saturday
	)
	if sat.Weekday() != time.Friday {
		t.Errorf("previous trading day from Saturday should be Friday, got %v", sat.Weekday())
	}
}

func containsAny(s string, substrs ...string) bool {
	for _, sub := range substrs {
		if len(sub) > 0 && len(s) > 0 {
			lower := strings.ToLower(s)
			if strings.Contains(lower, strings.ToLower(sub)) {
				return true
			}
		}
	}
	return false
}
```

Add `"strings"` to imports.

- [ ] **Step 2: Run tests (expect fail)**

Run: `CGO_ENABLED=1 go test ./internal/journal/ -v 2>&1 | head -10`
Expected: compilation error.

- [ ] **Step 3: Implement `internal/journal/journal.go`**

```go
package journal

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/camuig/rus-trader/internal/logger"
	"github.com/camuig/rus-trader/internal/storage"
)

// Lesson is a structured insight from trade analysis.
type Lesson struct {
	Pattern        string   `json:"pattern"`
	Observation    string   `json:"observation"`
	Recommendation string   `json:"recommendation"`
	Confidence     string   `json:"confidence"` // "low" | "medium" | "high"
	Tickers        []string `json:"tickers,omitempty"`
}

type Journal struct {
	repo   *storage.Repository
	logger *logger.Logger
}

func New(repo *storage.Repository, log *logger.Logger) *Journal {
	return &Journal{repo: repo, logger: log}
}

// RecordOutcome creates a TradeOutcome entry for a closed trade.
func (j *Journal) RecordOutcome(buyTrade *storage.Trade, sellPrice float64, pnl float64, sellReasoning string) {
	holdHours := time.Since(buyTrade.CreatedAt).Hours()
	outcome := "loss"
	if pnl > 0 {
		outcome = "win"
	} else if pnl == 0 {
		outcome = "breakeven"
	}

	whatHappened := ComputeWhatHappened(buyTrade.Price, sellPrice, holdHours)

	record := &storage.TradeOutcome{
		TradeID:           buyTrade.ID,
		Hypothesis:        buyTrade.Reasoning,
		EntryFeatures:     buyTrade.EntryFeatures,
		ExitReason:        ClassifyExitReason(sellReasoning),
		Outcome:           outcome,
		PnL:               pnl,
		HoldDurationHours: holdHours,
		WhatHappened:      whatHappened,
	}
	if err := j.repo.SaveTradeOutcome(record); err != nil {
		j.logger.Error("save trade outcome", "error", err)
	}
}

// NeedsDailyReview checks if a review for the previous trading day hasn't been done.
func (j *Journal) NeedsDailyReview(loc *time.Location) bool {
	now := time.Now().In(loc)
	prevDay := PreviousTradingDay(now)
	dateStr := prevDay.Format("2006-01-02")

	has, err := j.repo.HasReviewForDate(dateStr)
	if err != nil {
		j.logger.Error("check daily review", "error", err)
		return false
	}
	return !has
}

// PreviousTradingDay returns the most recent weekday before the given time.
func PreviousTradingDay(t time.Time) time.Time {
	prev := t.AddDate(0, 0, -1)
	for prev.Weekday() == time.Saturday || prev.Weekday() == time.Sunday {
		prev = prev.AddDate(0, 0, -1)
	}
	return prev
}

// GetPendingOutcomes returns outcomes that haven't been reviewed yet.
func (j *Journal) GetPendingOutcomes() ([]storage.TradeOutcome, error) {
	return j.repo.GetOutcomesSinceLastReview()
}

// SaveReview persists the LLM review result.
func (j *Journal) SaveReview(date string, lessons []Lesson, rawResponse string, tradesReviewed int) error {
	lessonsJSON, err := json.Marshal(lessons)
	if err != nil {
		return fmt.Errorf("marshal lessons: %w", err)
	}
	return j.repo.SaveJournalLesson(&storage.JournalLesson{
		ReviewDate:     date,
		LessonsJSON:    string(lessonsJSON),
		RawResponse:    rawResponse,
		TradesReviewed: tradesReviewed,
	})
}

// LoadLessons returns the most recent lessons filtered by age and confidence.
func (j *Journal) LoadLessons(maxAgeDays int, maxCount int) ([]Lesson, error) {
	records, err := j.repo.GetLatestLessons(maxAgeDays, 3) // up to 3 recent review sessions
	if err != nil {
		return nil, err
	}

	var allLessons []Lesson
	for _, r := range records {
		var lessons []Lesson
		if err := json.Unmarshal([]byte(r.LessonsJSON), &lessons); err != nil {
			continue
		}
		for _, l := range lessons {
			if l.Confidence == "low" {
				continue // skip low-confidence lessons
			}
			allLessons = append(allLessons, l)
		}
	}

	if len(allLessons) > maxCount {
		allLessons = allLessons[:maxCount]
	}
	return allLessons, nil
}

// ClassifyExitReason determines exit type from the SELL reasoning text.
func ClassifyExitReason(reasoning string) string {
	lower := strings.ToLower(reasoning)
	switch {
	case strings.Contains(lower, "стоп-лосс") || strings.Contains(lower, "stop-loss") ||
		strings.Contains(lower, "пробила sl") || strings.Contains(lower, "sl triggered") ||
		strings.Contains(lower, "стоп лосс"):
		return "sl_hit"
	case strings.Contains(lower, "тейк-профит") || strings.Contains(lower, "take profit") ||
		strings.Contains(lower, "take-profit") || strings.Contains(lower, "фиксируем прибыль") ||
		strings.Contains(lower, "tp достигнут") || strings.Contains(lower, "tp reached"):
		return "tp_reached"
	case strings.Contains(lower, "trailing"):
		return "trailing_stop"
	case strings.Contains(lower, "orphan") || strings.Contains(lower, "reconcile"):
		return "orphan_cleanup"
	default:
		return "ai_sell"
	}
}

// ComputeWhatHappened generates a brief text about what happened with the price.
func ComputeWhatHappened(entryPrice, exitPrice, holdHours float64) string {
	if entryPrice <= 0 {
		return ""
	}
	changePct := (exitPrice - entryPrice) / entryPrice * 100
	direction := "выросла"
	if changePct < 0 {
		direction = "упала"
	}
	return fmt.Sprintf("Цена %s на %+.2f%% за %.1f ч (вход %.2f → выход %.2f)",
		direction, changePct, holdHours, entryPrice, exitPrice)
}
```

- [ ] **Step 4: Run tests**

Run: `CGO_ENABLED=1 go test ./internal/journal/ -v`
Expected: all tests pass.

- [ ] **Step 5: Commit**

```bash
git add internal/journal/
git commit -m "feat: add trade journal with outcome recording and lesson management"
```

---

## Task 6: AI Screening Agent — Types, Prompt, DeepSeek Method

**Files:**
- Modify: `internal/ai/types.go`
- Create: `internal/ai/prompt_screening.go`
- Modify: `internal/ai/deepseek.go`

- [ ] **Step 1: Add new types to `internal/ai/types.go`**

Add at end of file:

```go
// ScreeningRequest is input for the Screening Agent (BUY candidates only).
type ScreeningRequest struct {
	TickerFeatures []string          // textual features per ticker
	Market         MarketContext
	GlobalNews     []string
	TickerNews     map[string][]string
	Lessons        []string          // formatted lesson lines
	TodayTraded    []string
	Stats          PerformanceStats
	CurrentTime    time.Time
	AvailableRub   float64
}

// PositionContext is a single open position for Position Manager.
type PositionContext struct {
	Ticker       string
	EntryPrice   float64
	CurrentPrice float64
	PnLPct       float64
	Quantity     int64
	HoldDuration string
	StopLoss     float64
	TakeProfit   float64
	ProgressToTP float64
	Hypothesis   string
	Features     string   // current textual features
	News         []string
}

// PositionRequest is input for the Position Manager Agent.
type PositionRequest struct {
	Positions   []PositionContext
	Lessons     []string // formatted lesson lines, filtered to position tickers
	CurrentTime time.Time
	Market      MarketContext
}

// JournalReviewRequest is input for the daily Trade Journal review.
type JournalReviewRequest struct {
	Outcomes        []JournalOutcome
	Stats           JournalReviewStats
	PreviousLessons []string // from last review
	CurrentDate     time.Time
}

type JournalOutcome struct {
	Ticker        string
	Hypothesis    string
	EntryFeatures string
	ExitReason    string
	Outcome       string // "win" | "loss" | "breakeven"
	PnL           float64
	HoldHours     float64
	WhatHappened  string
}

type JournalReviewStats struct {
	TotalTrades      int
	WinRate          float64
	AvgWinPnL        float64
	AvgLossPnL       float64
	AvgHoldHoursWin  float64
	AvgHoldHoursLoss float64
	ByExitReason     map[string]int
}
```

- [ ] **Step 2: Create `internal/ai/prompt_screening.go`**

```go
package ai

import (
	"fmt"
	"strings"
	"time"
)

const screeningSystemPrompt = `Роль: Аналитик-скринер MOEX (горизонт 1-3 дня).
Задача: Из предложенных тикеров отбери лучшие BUY-сетапы.

Ты НЕ управляешь позициями — только ищешь новые входы.

Как оценивать:
- Качество тренда и импульса.
- Подтверждение объёмом (повышенный объём усиливает сигнал).
- Близость к уровням поддержки/сопротивления.
- Дивидендный фактор (предстоящая отсечка = катализатор роста).
- Новостной фон (позитив/негатив по тикеру и рынку).
- Уроки из прошлых сделок — не повторяй известные ошибки.

Confidence:
  85-95: трендовый сетап + объём + катализатор (новости или дивиденд).
  70-84: рабочий сетап, один фактор неопределённости.
  <70: не возвращай — будет отсеяно.

SL/TP:
  SL строго ниже цены входа. Рассчитывай через ATR.
  TP — реалистичный, с учётом сопротивления.

Формат: JSON массив BUY-решений. Пустой [] — если ни один тикер не проходит фильтр.
[{"action":"BUY","ticker":"SBER","stop_loss":250.0,"take_profit":290.0,"confidence":80,"reasoning":"Причина"}]
`

func BuildScreeningPrompt(req *ScreeningRequest, maxChars int) string {
	if maxChars <= 0 {
		maxChars = 16000
	}

	var sb strings.Builder

	// Time
	if !req.CurrentTime.IsZero() {
		sb.WriteString(fmt.Sprintf("## Время: %s MSK\n\n", req.CurrentTime.Format("02.01.2006 15:04")))
	}

	// Market context
	if req.Market.IndexTicker != "" {
		sb.WriteString(fmt.Sprintf("## Фон рынка: %s: 1д %+.2f%%, 3д %+.2f%%, 1н %+.2f%% — %s\n\n",
			req.Market.IndexTicker, req.Market.ChangePct1d, req.Market.ChangePct3d, req.Market.ChangePct1w, req.Market.Regime))
	}

	// Stats
	if req.Stats.TradeCount7d > 0 {
		sb.WriteString("## Статистика 7 дней\n")
		sb.WriteString(fmt.Sprintf("Сделок: %d, Win rate: %.0f%%, P&L: %+.0f ₽\n",
			req.Stats.TradeCount7d, req.Stats.WinRate7d, req.Stats.TotalPnL7d))
		if req.Stats.LosingStreak >= 3 {
			sb.WriteString(fmt.Sprintf("⚠ %d убыточных подряд — повышай планку.\n", req.Stats.LosingStreak))
		}
		sb.WriteString("\n")
	}

	// Budget
	sb.WriteString(fmt.Sprintf("## Бюджет: %.0f ₽ доступно\n\n", req.AvailableRub))

	// Lessons
	if len(req.Lessons) > 0 {
		sb.WriteString("## Уроки из последних сделок\n")
		for _, l := range req.Lessons {
			sb.WriteString("- ")
			sb.WriteString(l)
			sb.WriteString("\n")
		}
		sb.WriteString("\n")
	}

	// Anti-churning
	if len(req.TodayTraded) > 0 {
		sb.WriteString("## Сегодня уже торговали (НЕ покупать): ")
		sb.WriteString(strings.Join(req.TodayTraded, ", "))
		sb.WriteString("\n\n")
	}

	// Ticker features
	sb.WriteString("## Тикеры\n")
	for _, f := range req.TickerFeatures {
		sb.WriteString(f)
		sb.WriteString("\n")
	}
	sb.WriteString("\n")

	// Ticker news
	if len(req.TickerNews) > 0 {
		sb.WriteString("## Новости по тикерам\n")
		for ticker, news := range req.TickerNews {
			for _, n := range news {
				sb.WriteString(fmt.Sprintf("- %s: %s\n", ticker, n))
			}
		}
		sb.WriteString("\n")
	}

	// Global news
	if len(req.GlobalNews) > 0 {
		sb.WriteString("## Общий фон\n")
		for _, n := range req.GlobalNews {
			sb.WriteString("- ")
			sb.WriteString(n)
			sb.WriteString("\n")
		}
		sb.WriteString("\n")
	}

	sb.WriteString("\nОтбери лучшие BUY-сетапы в JSON.")

	result := sb.String()
	if len([]rune(result)) > maxChars {
		r := []rune(result)
		result = string(r[:maxChars-1]) + "…"
	}
	return result
}
```

- [ ] **Step 3: Add ScreeningAnalyze to `internal/ai/deepseek.go`**

Add method after existing `Analyze()`:

```go
// ScreeningAnalyze calls DeepSeek for BUY candidate screening.
func (d *DeepSeekClient) ScreeningAnalyze(ctx context.Context, req *ScreeningRequest) ([]AIDecision, string, error) {
	userPrompt := BuildScreeningPrompt(req, d.cfg.AIAgents.Screening.MaxChars)

	d.logger.Info("screening agent prompt built",
		"chars", len([]rune(userPrompt)), "tickers", len(req.TickerFeatures))

	rawResponse, err := d.callLLM(ctx, screeningSystemPrompt, userPrompt, d.model, d.cfg.DeepSeek.TimeoutSeconds)
	if err != nil {
		return nil, "", fmt.Errorf("screening agent: %w", err)
	}

	decisions, err := ParseDecisions(rawResponse)
	if err != nil {
		return nil, rawResponse, fmt.Errorf("parse screening decisions: %w", err)
	}

	// Filter: only BUY decisions from screening agent
	var buys []AIDecision
	for _, d := range decisions {
		if d.Action == "BUY" {
			buys = append(buys, d)
		}
	}
	return buys, rawResponse, nil
}
```

Also extract the LLM call into a shared helper (to avoid duplication with PositionAnalyze and JournalReview). Add this private method:

```go
// callLLM executes a streaming chat completion with the given system/user prompts.
func (d *DeepSeekClient) callLLM(ctx context.Context, systemPrompt, userPrompt, model string, timeoutSec int) (string, error) {
	if model == "" {
		model = d.model
	}
	timeout := time.Duration(timeoutSec) * time.Second
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	stream, err := d.client.CreateChatCompletionStream(ctx, openai.ChatCompletionRequest{
		Model: model,
		Messages: []openai.ChatCompletionMessage{
			{Role: openai.ChatMessageRoleSystem, Content: systemPrompt},
			{Role: openai.ChatMessageRoleUser, Content: userPrompt},
		},
		Stream: true,
	})
	if err != nil {
		return "", fmt.Errorf("create stream: %w", err)
	}
	defer stream.Close()

	var content strings.Builder
	for {
		chunk, err := stream.Recv()
		if err != nil {
			if err == io.EOF {
				break
			}
			if content.Len() > 0 {
				break // partial response is usable
			}
			return "", fmt.Errorf("stream recv: %w", err)
		}
		if len(chunk.Choices) > 0 {
			content.WriteString(chunk.Choices[0].Delta.Content)
		}
	}
	return content.String(), nil
}
```

Add `"io"` to deepseek.go imports if not already present.

Refactor the existing `Analyze()` method to use `callLLM()` internally (replace the duplicate streaming code with a single call to `d.callLLM()`).

- [ ] **Step 4: Build and verify**

Run: `CGO_ENABLED=1 go build ./...`
Expected: clean build.

- [ ] **Step 5: Commit**

```bash
git add internal/ai/types.go internal/ai/prompt_screening.go internal/ai/deepseek.go
git commit -m "feat: add Screening Agent prompt and DeepSeek method"
```

---

## Task 7: AI Position Manager — Prompt and DeepSeek Method

**Files:**
- Create: `internal/ai/prompt_position.go`
- Modify: `internal/ai/deepseek.go`

- [ ] **Step 1: Create `internal/ai/prompt_position.go`**

```go
package ai

import (
	"fmt"
	"strings"
)

const positionSystemPrompt = `Роль: Управляющий позициями MOEX.
Задача: Для каждой открытой позиции реши — HOLD или SELL.

HOLD — дефолт. Позиция должна работать.
SELL только если:
  (а) Цена у стоп-лосса и структура сломана (тренд развернулся, поддержка пробита).
  (б) Серьёзный фундаментальный негатив (не просто "импульс ослабел").
  (в) Позиция удерживается >2 дней без прогресса к TP.
  (г) Оригинальная гипотеза входа опровергнута фактами.

НЕ закрывай прибыльные трендовые позиции.
НЕ закрывай ради "ротации" или "ребалансировки".

Для каждой позиции верни HOLD или SELL с reasoning.
Формат: JSON массив. HOLD тоже включай — чтобы было понятно что ты их видел.
[{"action":"HOLD","ticker":"SBER","confidence":0,"reasoning":"Причина"},
 {"action":"SELL","ticker":"GAZP","confidence":0,"reasoning":"Причина"}]
`

func BuildPositionPrompt(req *PositionRequest, maxChars int) string {
	if maxChars <= 0 {
		maxChars = 8000
	}

	var sb strings.Builder

	if !req.CurrentTime.IsZero() {
		sb.WriteString(fmt.Sprintf("## Время: %s MSK\n\n", req.CurrentTime.Format("02.01.2006 15:04")))
	}

	if req.Market.IndexTicker != "" {
		sb.WriteString(fmt.Sprintf("## Рынок: %s — %s (1д %+.2f%%)\n\n",
			req.Market.IndexTicker, req.Market.Regime, req.Market.ChangePct1d))
	}

	if len(req.Lessons) > 0 {
		sb.WriteString("## Уроки\n")
		for _, l := range req.Lessons {
			sb.WriteString("- ")
			sb.WriteString(l)
			sb.WriteString("\n")
		}
		sb.WriteString("\n")
	}

	sb.WriteString("## Открытые позиции\n\n")
	for _, p := range req.Positions {
		sb.WriteString(fmt.Sprintf("### %s\n", p.Ticker))
		sb.WriteString(fmt.Sprintf("Вход: %.2f, текущая: %.2f, P&L: %+.2f%%\n", p.EntryPrice, p.CurrentPrice, p.PnLPct))
		sb.WriteString(fmt.Sprintf("Кол-во: %d, удержание: %s\n", p.Quantity, p.HoldDuration))
		sb.WriteString(fmt.Sprintf("SL: %.2f, TP: %.2f (прогресс: %.0f%%)\n", p.StopLoss, p.TakeProfit, p.ProgressToTP))
		if p.Hypothesis != "" {
			sb.WriteString(fmt.Sprintf("Гипотеза: %s\n", p.Hypothesis))
		}
		if p.Features != "" {
			sb.WriteString(fmt.Sprintf("Текущее: %s\n", p.Features))
		}
		if len(p.News) > 0 {
			for _, n := range p.News {
				sb.WriteString(fmt.Sprintf("  Новость: %s\n", n))
			}
		}
		sb.WriteString("\n")
	}

	sb.WriteString("Для каждой позиции: HOLD или SELL с причиной.")

	result := sb.String()
	if len([]rune(result)) > maxChars {
		r := []rune(result)
		result = string(r[:maxChars-1]) + "…"
	}
	return result
}
```

- [ ] **Step 2: Add PositionAnalyze to `internal/ai/deepseek.go`**

```go
// PositionAnalyze calls DeepSeek for open position management (HOLD/SELL).
func (d *DeepSeekClient) PositionAnalyze(ctx context.Context, req *PositionRequest) ([]AIDecision, string, error) {
	userPrompt := BuildPositionPrompt(req, d.cfg.AIAgents.PositionManager.MaxChars)

	d.logger.Info("position manager prompt built",
		"chars", len([]rune(userPrompt)), "positions", len(req.Positions))

	rawResponse, err := d.callLLM(ctx, positionSystemPrompt, userPrompt, d.model, d.cfg.DeepSeek.TimeoutSeconds)
	if err != nil {
		return nil, "", fmt.Errorf("position manager: %w", err)
	}

	decisions, err := ParseDecisions(rawResponse)
	if err != nil {
		return nil, rawResponse, fmt.Errorf("parse position decisions: %w", err)
	}

	// Filter: only HOLD and SELL from position manager
	var filtered []AIDecision
	for _, d := range decisions {
		if d.Action == "HOLD" || d.Action == "SELL" {
			filtered = append(filtered, d)
		}
	}
	return filtered, rawResponse, nil
}
```

- [ ] **Step 3: Build and verify**

Run: `CGO_ENABLED=1 go build ./...`
Expected: clean build.

- [ ] **Step 4: Commit**

```bash
git add internal/ai/prompt_position.go internal/ai/deepseek.go
git commit -m "feat: add Position Manager agent prompt and DeepSeek method"
```

---

## Task 8: AI Journal Review — Prompt and DeepSeek Method

**Files:**
- Create: `internal/ai/prompt_journal.go`
- Modify: `internal/ai/deepseek.go`

- [ ] **Step 1: Create `internal/ai/prompt_journal.go`**

```go
package ai

import (
	"fmt"
	"strings"
)

const journalSystemPrompt = `Роль: Аналитик торговых результатов.
Задача: Проанализируй закрытые сделки и сформулируй 3-5 конкретных уроков.

Для каждого урока:
- pattern: какой паттерн/сетап/ситуация
- observation: что показали данные
- recommendation: что делать иначе
- confidence: "low" (1-2 сделки), "medium" (3-4), "high" (5+)

Примеры хороших уроков:
- "Пробой resistance на низком объёме (RelVol < 1.3) в 4 из 5 случаев оказался ложным → повысить порог объёма для breakout-сетапов"
- "Позиции открытые в первый час торгов имеют win rate 35% → предпочитать входы после 12:00"
- "SELL по AI с reasoning 'ослабление импульса' в 6 из 8 случаев был преждевременным → не закрывать по ослаблению импульса, ждать пробоя SL"

Формат: ТОЛЬКО JSON массив.
[{"pattern":"...","observation":"...","recommendation":"...","confidence":"medium","tickers":["SBER"]}]
`

func BuildJournalReviewPrompt(req *JournalReviewRequest, maxChars int) string {
	if maxChars <= 0 {
		maxChars = 12000
	}

	var sb strings.Builder

	sb.WriteString(fmt.Sprintf("## Обзор сделок за %s\n\n", req.CurrentDate.Format("02.01.2006")))

	// Stats
	sb.WriteString("## Агрегат\n")
	sb.WriteString(fmt.Sprintf("Сделок: %d, Win rate: %.0f%%\n", req.Stats.TotalTrades, req.Stats.WinRate))
	if req.Stats.AvgWinPnL > 0 {
		sb.WriteString(fmt.Sprintf("Ср. прибыль: +%.0f ₽ (удержание %.1f ч)\n", req.Stats.AvgWinPnL, req.Stats.AvgHoldHoursWin))
	}
	if req.Stats.AvgLossPnL < 0 {
		sb.WriteString(fmt.Sprintf("Ср. убыток: %.0f ₽ (удержание %.1f ч)\n", req.Stats.AvgLossPnL, req.Stats.AvgHoldHoursLoss))
	}
	if len(req.Stats.ByExitReason) > 0 {
		sb.WriteString("По причинам выхода: ")
		var reasons []string
		for reason, count := range req.Stats.ByExitReason {
			reasons = append(reasons, fmt.Sprintf("%s=%d", reason, count))
		}
		sb.WriteString(strings.Join(reasons, ", "))
		sb.WriteString("\n")
	}
	sb.WriteString("\n")

	// Individual outcomes
	sb.WriteString("## Сделки\n\n")
	for _, o := range req.Outcomes {
		sb.WriteString(fmt.Sprintf("### %s (%s, %+.0f ₽)\n", o.Ticker, o.Outcome, o.PnL))
		if o.Hypothesis != "" {
			sb.WriteString(fmt.Sprintf("Гипотеза: %s\n", o.Hypothesis))
		}
		if o.EntryFeatures != "" {
			sb.WriteString(fmt.Sprintf("При входе: %s\n", o.EntryFeatures))
		}
		sb.WriteString(fmt.Sprintf("Выход: %s. %s\n", o.ExitReason, o.WhatHappened))
		sb.WriteString(fmt.Sprintf("Удержание: %.1f ч\n\n", o.HoldHours))
	}

	// Previous lessons for continuity
	if len(req.PreviousLessons) > 0 {
		sb.WriteString("## Предыдущие уроки (для контекста)\n")
		for _, l := range req.PreviousLessons {
			sb.WriteString("- ")
			sb.WriteString(l)
			sb.WriteString("\n")
		}
		sb.WriteString("\n")
	}

	sb.WriteString("Сформулируй 3-5 уроков в JSON.")

	result := sb.String()
	if len([]rune(result)) > maxChars {
		r := []rune(result)
		result = string(r[:maxChars-1]) + "…"
	}
	return result
}
```

- [ ] **Step 2: Add JournalReview to `internal/ai/deepseek.go`**

```go
// JournalReview calls DeepSeek to analyze recent closed trades and generate lessons.
func (d *DeepSeekClient) JournalReview(ctx context.Context, req *JournalReviewRequest) ([]journal.Lesson, string, error) {
	model := d.model
	if d.cfg.AIAgents.JournalReview.Model != "" {
		model = d.cfg.AIAgents.JournalReview.Model
	}

	userPrompt := BuildJournalReviewPrompt(req, d.cfg.AIAgents.JournalReview.MaxChars)

	d.logger.Info("journal review prompt built",
		"chars", len([]rune(userPrompt)), "outcomes", len(req.Outcomes))

	rawResponse, err := d.callLLM(ctx, journalSystemPrompt, userPrompt, model, d.cfg.DeepSeek.TimeoutSeconds)
	if err != nil {
		return nil, "", fmt.Errorf("journal review: %w", err)
	}

	// Parse lessons from response
	cleaned := StripThinkTags(rawResponse)
	cleaned = stripCodeFences(cleaned)
	var lessons []journal.Lesson
	if err := json.Unmarshal([]byte(cleaned), &lessons); err != nil {
		// Try to extract JSON array from response
		if start := strings.Index(cleaned, "["); start >= 0 {
			if end := strings.LastIndex(cleaned, "]"); end > start {
				_ = json.Unmarshal([]byte(cleaned[start:end+1]), &lessons)
			}
		}
	}

	return lessons, rawResponse, nil
}
```

Add `"encoding/json"` and `"github.com/camuig/rus-trader/internal/journal"` to deepseek.go imports.

Note: `StripThinkTags` and `stripCodeFences` are already in `parser.go`. Verify they're exported or accessible from the same package (they are — same `ai` package).

- [ ] **Step 3: Build and verify**

Run: `CGO_ENABLED=1 go build ./...`
Expected: clean build.

- [ ] **Step 4: Commit**

```bash
git add internal/ai/prompt_journal.go internal/ai/deepseek.go
git commit -m "feat: add Journal Review agent prompt and DeepSeek method"
```

---

## Task 9: Executor — Save Entry Features and Record Outcomes

**Files:**
- Modify: `internal/executor/executor.go`

- [ ] **Step 1: Add journal and features to Executor struct**

Update the Executor struct and constructor to include journal:

```go
type Executor struct {
	broker     *broker.BrokerClient
	repo       *storage.Repository
	notifier   *telegram.Notifier
	config     *config.Config
	logger     *logger.Logger
	indicators map[string]indicators.Indicators
	journal    *journal.Journal
	features   map[string]string // ticker → current textual features
}

func (e *Executor) SetFeatures(f map[string]string) {
	e.features = f
}

func (e *Executor) SetJournal(j *journal.Journal) {
	e.journal = j
}
```

Add import for `"github.com/camuig/rus-trader/internal/journal"`.

- [ ] **Step 2: Save entry features on BUY**

In `executeBuy()`, when building the Trade struct (around line 235), add `EntryFeatures`:

```go
	entryFeatures := ""
	if e.features != nil {
		entryFeatures = e.features[d.Ticker]
	}

	trade := &storage.Trade{
		Ticker:            d.Ticker,
		Action:            "BUY",
		Price:             executedPrice,
		Quantity:          result.ExecutedLots,
		OrderID:           result.OrderID,
		StopLossPrice:     slPrice,
		TakeProfitPrice:   tpPrice,
		StopLossOrderID:   slOrderID,
		TakeProfitOrderID: tpOrderID,
		Reasoning:         d.Reasoning,
		EntryFeatures:     entryFeatures,
		Status:            "open",
	}
```

- [ ] **Step 3: Record outcome on SELL**

In `executeSell()`, after PnL is calculated and trade is updated (after the existing `e.repo.UpdateTrade(openTrade)` call), add:

```go
	// Record trade outcome for journal
	if e.journal != nil {
		e.journal.RecordOutcome(openTrade, result.ExecutedPrice, pnl, d.Reasoning)
	}
```

- [ ] **Step 4: Build and verify**

Run: `CGO_ENABLED=1 go build ./...`
Expected: clean build.

- [ ] **Step 5: Commit**

```bash
git add internal/executor/executor.go
git commit -m "feat: executor saves entry features on BUY, records journal outcomes on SELL"
```

---

## Task 10: Scheduler — Dual AI Calls, Daily Review, Full Integration

**Files:**
- Modify: `internal/scheduler/scheduler.go`
- Modify: `cmd/bot/main.go` (if journal needs to be passed to scheduler)

This is the largest task. It wires everything together.

- [ ] **Step 1: Add dependencies to Scheduler struct**

Add `dividends`, `journal`, and `errgroup` imports. Update the struct:

```go
import (
	// existing imports...
	"golang.org/x/sync/errgroup"

	"github.com/camuig/rus-trader/internal/dividends"
	"github.com/camuig/rus-trader/internal/features"
	"github.com/camuig/rus-trader/internal/journal"
)

type Scheduler struct {
	broker    *broker.BrokerClient
	moex      *moex.Client
	ai        *ai.DeepSeekClient
	executor  *executor.Executor
	repo      *storage.Repository
	notifier  *telegram.Notifier
	guard     *guard.TradeGuard
	config    *config.Config
	logger    *logger.Logger
	loc       *time.Location
	divFetcher *dividends.Fetcher
	journal    *journal.Journal
}
```

Update `NewScheduler` to accept and store the new dependencies.

- [ ] **Step 2: Add daily review logic**

Add method to scheduler:

```go
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

	// Build review request
	prevLessons, _ := s.journal.LoadLessons(s.config.Journal.MaxLessonAgeDays, s.config.Journal.MaxLessonsInPrompt)
	var prevLessonLines []string
	for _, l := range prevLessons {
		prevLessonLines = append(prevLessonLines, fmt.Sprintf("[%s] %s: %s → %s", l.Confidence, l.Pattern, l.Observation, l.Recommendation))
	}

	// Build outcomes for prompt
	var journalOutcomes []ai.JournalOutcome
	var wins, losses int
	var winPnL, lossPnL, winHours, lossHours float64
	exitReasons := make(map[string]int)

	for _, o := range outcomes {
		journalOutcomes = append(journalOutcomes, ai.JournalOutcome{
			Ticker:        o.Ticker,  // Note: need to join with trade to get ticker
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

	lessons, rawResponse, err := s.ai.JournalReview(ctx, reviewReq)
	if err != nil {
		s.logger.Error("daily review: LLM call", "error", err)
		return
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

func safeDiv(a, b float64) float64 {
	if b == 0 {
		return 0
	}
	return a / b
}
```

- [ ] **Step 3: Replace single AI call with dual agents in `runCycle()`**

In the `runCycle()` method, find the section where AI analysis is called (around the comment `// 12. AI analysis`) and replace it with:

```go
	// 11b. Market regime context
	marketCtx := computeMarketContext(allSnapshots)

	// 11c. Daily journal review (first cycle of the day)
	s.runDailyReview(ctx)

	// 11d. Load journal lessons for AI agents
	var lessonLines []string
	if s.journal != nil && s.config.Journal.Enabled {
		lessons, err := s.journal.LoadLessons(s.config.Journal.MaxLessonAgeDays, s.config.Journal.MaxLessonsInPrompt)
		if err != nil {
			s.logger.Error("load lessons", "error", err)
		}
		for _, l := range lessons {
			lessonLines = append(lessonLines, fmt.Sprintf("[%s] %s: %s → %s", l.Confidence, l.Pattern, l.Observation, l.Recommendation))
		}
	}

	// 11e. Fetch dividends
	var divMap map[string]dividends.DividendInfo
	if s.divFetcher != nil && s.config.Dividends.Enabled {
		var err error
		divMap, err = s.divFetcher.Fetch(ctx)
		if err != nil {
			s.logger.Error("fetch dividends", "error", err)
		} else {
			divMap = dividends.FilterByLookahead(divMap, s.config.Dividends.LookaheadDays)
			s.logger.Info("dividends loaded", "count", len(divMap))
		}
	}

	// 11f. Build textual features (replaces raw OHLCV tables)
	var tickerFeatureTexts []string
	featuresMap := make(map[string]string) // for executor entry_features
	for _, snap := range snapshots {
		var div *dividends.DividendInfo
		if d, ok := divMap[snap.Ticker]; ok {
			div = &d
		}
		feat := features.BuildTickerFeatures(snap, div)
		tickerFeatureTexts = append(tickerFeatureTexts, feat.Summary)
		featuresMap[snap.Ticker] = feat.Summary
	}
	s.executor.SetFeatures(featuresMap)

	// 11g. Build ticker news map
	tickerNewsMap := make(map[string][]string)
	for ticker, items := range tickerNews {
		for _, n := range items {
			tickerNewsMap[ticker] = append(tickerNewsMap[ticker], n.Title)
		}
	}

	// 12. Dual AI analysis (parallel)
	s.logger.Info("starting dual AI analysis",
		"screening_tickers", len(tickerFeatureTexts),
		"open_positions", len(portfolio.Positions))

	var screeningDecisions, positionDecisions []ai.AIDecision
	var screeningRaw, positionRaw string

	g, gCtx := errgroup.WithContext(ctx)

	// Screening Agent — finds BUY candidates
	g.Go(func() error {
		screeningReq := &ai.ScreeningRequest{
			TickerFeatures: tickerFeatureTexts,
			Market:         marketCtx,
			GlobalNews:     globalNews,
			TickerNews:     tickerNewsMap,
			Lessons:        lessonLines,
			TodayTraded:    todayTraded,
			Stats:          stats,
			CurrentTime:    time.Now().In(s.loc),
			AvailableRub:   portfolio.AvailableRub,
		}
		var err error
		screeningDecisions, screeningRaw, err = s.ai.ScreeningAnalyze(gCtx, screeningReq)
		if err != nil {
			s.logger.Error("screening agent failed", "error", err)
		}
		return nil // don't fail the group
	})

	// Position Manager — manages open positions (only if there are any)
	if len(portfolio.Positions) > 0 {
		g.Go(func() error {
			var posContexts []ai.PositionContext
			for _, pos := range portfolio.Positions {
				pctChange := 0.0
				if pos.AvgPrice > 0 {
					pctChange = (pos.CurrentPrice - pos.AvgPrice) / pos.AvgPrice * 100
				}
				pc := ai.PositionContext{
					Ticker:       pos.Ticker,
					EntryPrice:   pos.AvgPrice,
					CurrentPrice: pos.CurrentPrice,
					PnLPct:       pctChange,
					Quantity:     int64(pos.Quantity),
					Features:     featuresMap[pos.Ticker],
				}
				if tc, ok := openContext[pos.Ticker]; ok {
					pc.StopLoss = tc.StopLossPrice
					pc.TakeProfit = tc.TakeProfitPrice
					pc.Hypothesis = tc.Reasoning
					pc.HoldDuration = formatDurationSince(tc.OpenedAt)
					if tc.TakeProfitPrice > 0 && pos.AvgPrice > 0 && tc.TakeProfitPrice != pos.AvgPrice {
						pc.ProgressToTP = (pos.CurrentPrice - pos.AvgPrice) / (tc.TakeProfitPrice - pos.AvgPrice) * 100
					}
				}
				if items, ok := tickerNewsMap[pos.Ticker]; ok {
					pc.News = items
				}
				posContexts = append(posContexts, pc)
			}

			posReq := &ai.PositionRequest{
				Positions:   posContexts,
				Lessons:     lessonLines, // TODO: could filter to position tickers only
				CurrentTime: time.Now().In(s.loc),
				Market:      marketCtx,
			}
			var err error
			positionDecisions, positionRaw, err = s.ai.PositionAnalyze(gCtx, posReq)
			if err != nil {
				s.logger.Error("position manager failed", "error", err)
			}
			return nil
		})
	}

	_ = g.Wait()

	// Merge decisions: position manager results + screening results
	decisions := append(positionDecisions, screeningDecisions...)
	rawResponse := ""
	if screeningRaw != "" {
		rawResponse = "=== SCREENING ===\n" + screeningRaw
	}
	if positionRaw != "" {
		rawResponse += "\n=== POSITION MANAGER ===\n" + positionRaw
	}

	s.logger.Info("AI decisions received",
		"screening", len(screeningDecisions), "position", len(positionDecisions), "total", len(decisions))
```

Add a helper:

```go
func formatDurationSince(t time.Time) string {
	d := time.Since(t)
	hours := int(d.Hours())
	if hours >= 24 {
		return fmt.Sprintf("%dд %dч", hours/24, hours%24)
	}
	return fmt.Sprintf("%dч %dм", hours, int(d.Minutes())%60)
}
```

Remove the old `Analyze()` call and the old `AnalysisRequest` construction code that it replaces.

- [ ] **Step 4: Update `cmd/bot/main.go`**

Initialize the new dependencies and pass them to scheduler. After the existing broker/AI/executor initialization, add:

```go
	// Dividend fetcher
	var divFetcher *dividends.Fetcher
	if cfg.Dividends.Enabled {
		divFetcher = dividends.NewFetcher(
			time.Duration(cfg.Dividends.CacheTTLHours)*time.Hour,
			log,
		)
	}

	// Trade journal
	var j *journal.Journal
	if cfg.Journal.Enabled {
		j = journal.New(repo, log)
	}
```

Pass `divFetcher` and `j` to `NewScheduler()`.

Add imports for `dividends` and `journal` packages.

- [ ] **Step 5: Add errgroup dependency**

Run: `go get golang.org/x/sync/errgroup`

- [ ] **Step 6: Build and run all tests**

Run: `CGO_ENABLED=1 go build ./... && CGO_ENABLED=1 go test ./...`
Expected: clean build, all tests pass.

- [ ] **Step 7: Commit**

```bash
git add internal/scheduler/ cmd/bot/main.go go.mod go.sum
git commit -m "feat: integrate dual AI agents, daily review, dividends, and features into scheduler"
```

---

## Task 11: Update Config Example and README

**Files:**
- Modify: `config.example.yaml` (already partially done in Task 1)
- Modify: `README.md`

- [ ] **Step 1: Verify `config.example.yaml` has all new sections**

Ensure the file has `dividends:`, `journal:`, and `ai_agents:` sections (added in Task 1). If anything is missing, add it.

- [ ] **Step 2: Update README.md**

Add a section describing the new Phase 1 features:

```markdown
### Фаза 1: Smart Trading Pipeline

#### Текстовые признаки вместо OHLCV-таблиц
Вместо сырых числовых таблиц AI получает осмысленные текстовые описания:
"SBER (305.20): аптренд 3-й день, объём 1.8x, RSI 58. У сопротивления 308.50 (0.5 ATR). Дивиденд 8.2% (отсечка через 12 дн)."
LLM работает с текстом значительно лучше, чем с таблицами чисел.

#### Дивидендный календарь (SmartLab)
Бот парсит SmartLab для получения дат отсечки, размера дивидендов и доходности. Дивидендная информация включается в текстовые признаки тикеров с предстоящей отсечкой (в пределах `dividends.lookahead_days`). Кеш обновляется раз в 12 часов.

#### Два AI-агента вместо одного
- **Screening Agent** — анализирует рыночные данные и ищет BUY-кандидаты. Не видит портфель.
- **Position Manager** — управляет открытыми позициями (HOLD/SELL). Не видит рыночные кандидаты.
Оба агента работают параллельно. Если один упал — другой продолжает. Результаты объединяются и проходят через Guard.

#### Trade Journal с LLM Daily Review
При каждой сделке сохраняется "outcome" — гипотеза при входе, текстовые признаки, причина выхода, результат.
Утром первого торгового дня LLM анализирует закрытые сделки и генерирует 3-5 структурированных уроков.
Уроки инжектятся в промпты обоих агентов, позволяя боту учиться на своих ошибках.
```

- [ ] **Step 3: Commit**

```bash
git add config.example.yaml README.md
git commit -m "docs: update README and config for Phase 1 smart trading pipeline"
```

---

## Verification Checklist

After all tasks are complete, run final verification:

- [ ] `CGO_ENABLED=1 go build ./...` — clean build
- [ ] `CGO_ENABLED=1 go test ./...` — all tests pass
- [ ] `CGO_ENABLED=1 go vet ./...` — no warnings
- [ ] Start bot with `go run ./cmd/bot/ -config config.yaml` and verify:
  - Dividends are fetched (log: "dividends fetched from SmartLab")
  - Textual features appear in log (log: "screening agent prompt built")
  - Both AI agents are called (log: "screening agent...", "position manager...")
  - Daily review triggers on first cycle (log: "running daily journal review" or "not enough trades")
  - Guard and executor work as before
