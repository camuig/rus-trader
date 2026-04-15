# Phase 2: Data Enrichment — Order Book, Sentiment, Vector Pattern Memory

**Date:** 2026-04-15
**Status:** Approved
**Scope:** Order book metrics, LLM sentiment scoring, SQLite vector pattern memory with OpenRouter embeddings
**Depends on:** Phase 1 (textual features, dual AI agents, trade journal)

## Overview

Phase 2 adds three new data sources to the trading pipeline, each enriching the Screening Agent's context:

1. **Order Book Metrics** — bid/ask imbalance, spread, wall detection from L2 snapshots
2. **Sentiment Scoring** — LLM-scored sentiment from existing news + SmartLab forum posts
3. **Vector Pattern Memory** — find historically similar setups via embeddings and report win/loss stats

All three are fully optional (graceful degradation). If any component is disabled or fails, the bot operates as in Phase 1.

### Non-Goals

- Streaming order book (Phase 3)
- Trades stream / institutional flow detection (Phase 3)
- External vector DB (Qdrant/ChromaDB)
- Real-time sentiment tracking

## 1. Order Book Metrics (`internal/orderbook/`)

### 1.1 Data Source

T-Invest SDK: `MarketDataServiceClient.GetOrderBook(instrumentUID, depth=20)` — returns bid/ask arrays with price + volume per level.

Already used in the codebase with depth=1 for spread calculation (`broker/portfolio.go:GetSpreadPct`).

### 1.2 New Broker Method

Add to `internal/broker/portfolio.go`:

```go
type OrderBookLevel struct {
    Price  float64
    Volume int64
}

type OrderBookRaw struct {
    Bids     []OrderBookLevel
    Asks     []OrderBookLevel
    BestBid  float64
    BestAsk  float64
}

func (bc *BrokerClient) GetOrderBookFull(instrumentUID string, depth int) (*OrderBookRaw, error)
```

Calls `GetOrderBook(instrumentUID, depth)`, converts proto `Quotation` to float64 for each level.

### 1.3 Metrics

```go
// internal/orderbook/metrics.go

type OrderBookMetrics struct {
    Ticker          string
    BidAskImbalance float64  // sum(bid_vol) / sum(ask_vol); >1 = buy pressure
    SpreadPct       float64  // (bestAsk - bestBid) / bestBid * 100
    BidWall         *Wall    // nil if no wall detected
    AskWall         *Wall    // nil if no wall detected
}

type Wall struct {
    Price  float64
    Volume int64
    Ratio  float64 // volume / avg_level_volume; wall if >= 5.0
}

func ComputeMetrics(ticker string, book *broker.OrderBookRaw) OrderBookMetrics
```

**BidAskImbalance:** `sum(bid_volumes) / sum(ask_volumes)`. If ask_total == 0, imbalance = 10 (capped). If bid_total == 0, imbalance = 0.

**Wall detection:** For each side (bids, asks), compute average volume per level. Any level with volume >= 5 * average is a wall. Report the largest wall on each side. If no level meets threshold, wall is nil.

### 1.4 Integration into Features

`features.BuildTickerFeatures` gains a new parameter `ob *orderbook.OrderBookMetrics` (nil if unavailable).

Feature text output:
- Imbalance > 1.5: `"стакан: bid давление 1.8x"`
- Imbalance < 0.7: `"стакан: ask давление (imb 0.5)"`
- Between: `"стакан: нейтральный"`
- SpreadPct: always shown: `"спред 0.12%"`
- Wall: `"bid wall на 298.50 (7.2x)"` / `"ask wall на 312.00 (5.3x)"`

Example: `"стакан: bid давление 1.8x, спред 0.12%, bid wall на 298.50 (7.2x)"`

### 1.5 Scheduler Integration

In `runCycle()`, after candle snapshots are fetched and screened, fetch order book for each screened ticker (parallel, using errgroup with concurrency limit):

```go
// Fetch order book for screened tickers (parallel, max 5 concurrent)
obMetrics := s.fetchOrderBooks(ctx, snapshots)
```

Method fetches `GetOrderBookFull(uid, 20)` for each ticker, computes `orderbook.ComputeMetrics`, returns `map[string]*orderbook.OrderBookMetrics`.

On error per ticker: log warning, that ticker gets nil metrics. On total failure: all metrics nil, features work without order book data.

### 1.6 Config

```yaml
orderbook:
  enabled: true
  depth: 20              # order book depth (levels)
  wall_threshold: 5.0    # volume/avg ratio to detect wall
  concurrency: 5         # parallel order book fetches
```

```go
type OrderBookConfig struct {
    Enabled       bool    `yaml:"enabled"`
    Depth         int     `yaml:"depth"`
    WallThreshold float64 `yaml:"wall_threshold"`
    Concurrency   int     `yaml:"concurrency"`
}
```

## 2. Sentiment Scoring (`internal/sentiment/`)

### 2.1 Data Sources

**Existing (no new code):**
- Finam news per ticker (`moex.FilterNewsForTickers`) — already fetched in scheduler
- World news (`moex.FetchWorldNews`) — already fetched

**New:**
- SmartLab forum posts per ticker: `https://smart-lab.ru/forum/{TICKER}/` — parse last 5-10 post titles from HTML.

### 2.2 SmartLab Forum Parser

```go
// internal/sentiment/smartlab_forum.go

type ForumPost struct {
    Title     string
    Author    string
    Timestamp time.Time
}

func FetchForumPosts(ctx context.Context, ticker string, maxPosts int) ([]ForumPost, error)
```

Parses `<table>` with forum topics. Extracts post titles (column with `<a>` links to topics). Filters posts older than 48h. Returns up to `maxPosts` (default 5).

### 2.3 LLM Sentiment Scoring

```go
// internal/sentiment/scorer.go

type SentimentResult struct {
    Ticker string
    Score  float64 // -1.0 to +1.0
    Label  string  // "very_negative" | "negative" | "neutral" | "positive" | "very_positive"
    Reason string  // brief explanation
}

type Scorer struct {
    aiClient *ai.DeepSeekClient
    config   *config.Config
    logger   *logger.Logger
}

func NewScorer(aiClient *ai.DeepSeekClient, cfg *config.Config, log *logger.Logger) *Scorer
func (s *Scorer) ScoreBatch(ctx context.Context, tickerTexts map[string][]string) (map[string]SentimentResult, error)
```

`ScoreBatch` takes `map[ticker][]headlines_and_posts` and makes a single LLM call to score all tickers at once.

**System prompt for sentiment scoring:**
```
Оцени sentiment по каждому тикеру на основе новостей и мнений трейдеров.
Шкала: -1.0 (крайне негативный) до +1.0 (крайне позитивный).
0 = нейтральный, нет значимых новостей.

Формат: JSON массив.
[{"ticker":"SBER","score":0.6,"reason":"Рекордные дивиденды, позитивные прогнозы"},
 {"ticker":"GAZP","score":-0.4,"reason":"Снижение добычи, неопределённость с дивидендами"}]
```

Uses cheap model (configurable, default `deepseek-chat` — no reasoning needed for sentiment).

### 2.4 Integration into Features

`features.BuildTickerFeatures` gains parameter `sentiment *sentiment.SentimentResult` (nil if unavailable).

Feature text:
- Score > 0.3: `"sentiment +0.6 (позитивный: рекордные дивиденды)"`
- Score < -0.3: `"sentiment -0.4 (негативный: снижение добычи)"`
- Between: `"sentiment нейтральный"`
- Nil: omitted

### 2.5 Scheduler Integration

In `runCycle()`, after news fetch and before feature building:

```go
// Sentiment scoring (LLM, non-fatal)
sentimentMap := s.scoreSentiment(ctx, tickerNews, forumPosts, globalNews)
```

Collects per-ticker texts (news titles + forum posts), calls `scorer.ScoreBatch`, returns `map[string]*sentiment.SentimentResult`.

On error: empty map, features work without sentiment.

### 2.6 Config

```yaml
sentiment:
  enabled: true
  model: "deepseek-chat"       # cheap model for scoring
  max_items_per_ticker: 5      # max news/posts per ticker for scoring
  forum_enabled: true           # fetch SmartLab forum posts
  max_forum_posts: 5
```

```go
type SentimentConfig struct {
    Enabled           bool   `yaml:"enabled"`
    Model             string `yaml:"model"`
    MaxItemsPerTicker int    `yaml:"max_items_per_ticker"`
    ForumEnabled      bool   `yaml:"forum_enabled"`
    MaxForumPosts     int    `yaml:"max_forum_posts"`
}
```

## 3. Vector Pattern Memory (`internal/vectordb/`)

### 3.1 SQLite Storage

New model in `internal/storage/models.go`:

```go
type PatternEmbedding struct {
    ID            uint      `gorm:"primarykey"`
    CreatedAt     time.Time
    TradeID       uint      `gorm:"not null;index"`
    Ticker        string    `gorm:"not null;index"`
    EntryFeatures string    `gorm:"type:text;not null"`
    Embedding     []byte    `gorm:"type:blob;not null"` // serialized []float32
    Outcome       string    // "win" | "loss" | "breakeven" | "" (still open)
    PnL           float64
    HoldHours     float64
}
```

Add to AutoMigrate. New repository methods:

```go
func (r *Repository) SavePatternEmbedding(pe *PatternEmbedding) error
func (r *Repository) UpdatePatternOutcome(tradeID uint, outcome string, pnl float64, holdHours float64) error
func (r *Repository) GetAllClosedEmbeddings() ([]PatternEmbedding, error)
```

### 3.2 Embedding Client (OpenRouter)

```go
// internal/vectordb/embeddings.go

type EmbeddingClient struct {
    client *openai.Client // go-openai with OpenRouter base URL
    model  string
    logger *logger.Logger
}

func NewEmbeddingClient(apiKey, model string, log *logger.Logger) *EmbeddingClient

func (c *EmbeddingClient) Embed(ctx context.Context, text string) ([]float32, error)
func (c *EmbeddingClient) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error)
```

Uses `go-openai` with `BaseURL: "https://openrouter.ai/api/v1"` and OpenRouter API key. Model default: `openai/text-embedding-3-small` (1536 dimensions).

### 3.3 In-Memory Index and Search

```go
// internal/vectordb/store.go

type PatternMatch struct {
    Ticker        string
    EntryFeatures string
    Outcome       string
    PnL           float64
    HoldHours     float64
    Similarity    float64
}

type Store struct {
    repo       *storage.Repository
    embedder   *EmbeddingClient
    logger     *logger.Logger
    patterns   []cachedPattern  // loaded at startup, updated on new entries
    mu         sync.RWMutex
}

type cachedPattern struct {
    embedding []float32
    ticker    string
    features  string
    outcome   string
    pnl       float64
    holdHours float64
}

func NewStore(repo *storage.Repository, embedder *EmbeddingClient, log *logger.Logger) *Store
func (s *Store) LoadAll() error                                    // load from DB into memory at startup
func (s *Store) SaveEntry(ctx context.Context, tradeID uint, ticker, features string) error  // embed + save
func (s *Store) UpdateOutcome(tradeID uint, outcome string, pnl, holdHours float64) error
func (s *Store) FindSimilar(ctx context.Context, features string, maxResults int, minSimilarity float64) ([]PatternMatch, error)
```

**LoadAll:** at startup, loads all `PatternEmbedding` with non-empty outcome from DB, deserializes embeddings into `[]float32`, stores in memory.

**SaveEntry:** called on BUY. Calls `embedder.Embed(features)`, saves to DB, adds to in-memory cache (with empty outcome).

**UpdateOutcome:** called on SELL. Updates DB record and in-memory cache with outcome/pnl/holdHours.

**FindSimilar:** called during screening. Embeds the query features, computes cosine similarity against all cached patterns with non-empty outcome, returns top matches above min similarity threshold.

### 3.4 Cosine Similarity

```go
// internal/vectordb/cosine.go

func CosineSimilarity(a, b []float32) float64
```

Serialization helpers:

```go
func SerializeEmbedding(v []float32) []byte     // binary encoding, 4 bytes per float32
func DeserializeEmbedding(b []byte) []float32
```

### 3.5 Integration into Screening Prompt

`BuildScreeningPrompt` gains a new parameter `patterns map[string][]PatternMatch` (ticker → similar patterns).

New section in prompt (after ticker features, before news):

```
## Похожие исторические паттерны
SBER: 5 похожих — 3 win (+avg 2.8%), 2 loss (-avg 1.5%). Win rate 60%.
  Ближайший (0.92): аптренд + дивиденд → win +3.1% за 18ч
  Второй (0.87): аптренд + объём → loss -2.0% за 6ч (SL)
GAZP: 2 похожих — 0 win, 2 loss. Win rate 0%. ⚠ Паттерн убыточный.
```

If no similar patterns for a ticker (none above min_similarity), that ticker is omitted from this section.

### 3.6 Executor Integration

**On BUY:** after trade is saved, call `vectorStore.SaveEntry(ctx, trade.ID, ticker, entryFeatures)`.

**On SELL:** after outcome is recorded, call `vectorStore.UpdateOutcome(buyTrade.ID, outcome, pnl, holdHours)`.

Both calls are non-fatal — errors are logged but don't block execution.

### 3.7 Config

```yaml
vectordb:
  enabled: true
  openrouter_api_key: "sk-or-..."
  embedding_model: "openai/text-embedding-3-small"
  max_similar_patterns: 5
  min_similarity: 0.7
```

```go
type VectorDBConfig struct {
    Enabled           bool    `yaml:"enabled"`
    OpenRouterAPIKey  string  `yaml:"openrouter_api_key"`
    EmbeddingModel    string  `yaml:"embedding_model"`
    MaxSimilarPatterns int    `yaml:"max_similar_patterns"`
    MinSimilarity     float64 `yaml:"min_similarity"`
}
```

## 4. Scheduler Orchestration

### Updated runCycle flow:

```
runCycle():
  1-5. [unchanged] tickers, candles, portfolio, reconcile, screener
  6.   [NEW] Order Book: fetch for screened tickers (parallel, max concurrency)
  7.   [unchanged] Finam + world news
  7a.  [NEW] SmartLab forum posts for screened tickers (parallel)
  8.   [NEW] Sentiment: batch LLM scoring (news + forum)
  9.   [unchanged] open context, today traded, stats, market context
  10.  [unchanged] daily review, load lessons, dividends
  11.  Build features (+ order book + sentiment)
  12.  [NEW] Vector search: for each screened ticker, find similar patterns
  13.  Screening Agent (+ patterns in prompt)
  14.  Position Manager
  15-17. Guard → Executor → Save

  On BUY: save embedding via vectorStore.SaveEntry
  On SELL: update embedding via vectorStore.UpdateOutcome
```

## 5. Error Handling

| Component | Failure | Behavior |
|-----------|---------|----------|
| GetOrderBookFull | API error / sandbox | Nil metrics for ticker. Features omit order book line. |
| SmartLab forum | HTTP/parse error | No posts. Sentiment scores from news only. |
| Sentiment LLM | Timeout/error | No sentiment in features. Log + Telegram alert. |
| OpenRouter embed | API error | Embedding not saved on BUY. Search skipped. Log. |
| Cosine search | No matches / empty DB | "Similar patterns" section omitted from prompt. |
| Any component disabled | config `enabled: false` | Fully skipped, zero overhead. |

## 6. Testing Strategy

| Package | Tests |
|---------|-------|
| `orderbook/` | Imbalance (symmetric, asymmetric, empty). Wall detection (threshold, no wall, both sides). Edge: zero volume, single level. |
| `sentiment/` | SmartLab HTML fixture parse. Label from score (-0.8 → very_negative, 0.1 → neutral, etc.). Prompt building for batch. |
| `vectordb/` | Cosine similarity (identical=1.0, orthogonal=0.0, opposite=-1.0). Serialize/deserialize roundtrip. Store save+search (SQLite in-memory). |
| `features/` | Features with order book (imbalance, wall). Features with sentiment. Features with both + dividend. Nil order book / nil sentiment. |

## 7. Files Changed Summary

| File | Change |
|------|--------|
| `internal/orderbook/metrics.go` | NEW |
| `internal/orderbook/metrics_test.go` | NEW |
| `internal/sentiment/smartlab_forum.go` | NEW |
| `internal/sentiment/scorer.go` | NEW |
| `internal/sentiment/sentiment_test.go` | NEW |
| `internal/vectordb/store.go` | NEW |
| `internal/vectordb/embeddings.go` | NEW |
| `internal/vectordb/cosine.go` | NEW |
| `internal/vectordb/store_test.go` | NEW |
| `internal/broker/portfolio.go` | MODIFIED: + GetOrderBookFull |
| `internal/features/builder.go` | MODIFIED: + OrderBookMetrics, SentimentResult params |
| `internal/features/builder_test.go` | MODIFIED: + tests with order book, sentiment |
| `internal/ai/types.go` | MODIFIED: + PatternMatch in ScreeningRequest |
| `internal/ai/prompt_screening.go` | MODIFIED: + similar patterns section |
| `internal/storage/models.go` | MODIFIED: + PatternEmbedding |
| `internal/storage/database.go` | MODIFIED: + AutoMigrate |
| `internal/storage/repository.go` | MODIFIED: + pattern embedding methods |
| `internal/config/config.go` | MODIFIED: + OrderBookConfig, SentimentConfig, VectorDBConfig |
| `internal/scheduler/scheduler.go` | MODIFIED: + order book, sentiment, vector search integration |
| `internal/executor/executor.go` | MODIFIED: + save/update embeddings |
| `cmd/bot/main.go` | MODIFIED: + init orderbook, sentiment, vectordb |
| `config.example.yaml` | MODIFIED: + new sections |
| `README.md` | MODIFIED: + Phase 2 docs |
