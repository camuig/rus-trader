# Phase 2: Data Enrichment — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add order book metrics, LLM sentiment scoring, and vector pattern memory to enrich the Screening Agent's context with real-time market microstructure, trader sentiment, and historical pattern similarity.

**Architecture:** Three new packages (`orderbook`, `sentiment`, `vectordb`) integrated into the existing Phase 1 pipeline. Each is optional (graceful degradation). Order book uses T-Invest `GetOrderBook` snapshots. Sentiment uses existing news + SmartLab forum + LLM batch scoring. Vector DB uses SQLite + OpenRouter embeddings + in-memory cosine search.

**Tech Stack:** Go 1.22, T-Invest SDK (gRPC), go-openai (for OpenRouter), SQLite/GORM, `golang.org/x/net/html` (already in deps)

**Spec:** `docs/superpowers/specs/2026-04-15-phase2-data-enrichment-design.md`

---

## Task 1: Configuration — New Config Structs

**Files:**
- Modify: `internal/config/config.go`
- Modify: `config.example.yaml`

- [ ] **Step 1: Add new config types to `internal/config/config.go`**

Add after existing `AIAgentsConfig` struct:

```go
type OrderBookConfig struct {
	Enabled       bool    `yaml:"enabled"`
	Depth         int     `yaml:"depth"`
	WallThreshold float64 `yaml:"wall_threshold"`
	Concurrency   int     `yaml:"concurrency"`
}

type SentimentConfig struct {
	Enabled           bool   `yaml:"enabled"`
	Model             string `yaml:"model"`
	MaxItemsPerTicker int    `yaml:"max_items_per_ticker"`
	ForumEnabled      bool   `yaml:"forum_enabled"`
	MaxForumPosts     int    `yaml:"max_forum_posts"`
}

type VectorDBConfig struct {
	Enabled            bool    `yaml:"enabled"`
	OpenRouterAPIKey   string  `yaml:"openrouter_api_key"`
	EmbeddingModel     string  `yaml:"embedding_model"`
	MaxSimilarPatterns int     `yaml:"max_similar_patterns"`
	MinSimilarity      float64 `yaml:"min_similarity"`
}
```

Add fields to Config struct:

```go
OrderBook  OrderBookConfig  `yaml:"orderbook"`
Sentiment  SentimentConfig  `yaml:"sentiment"`
VectorDB   VectorDBConfig   `yaml:"vectordb"`
```

- [ ] **Step 2: Add defaults in `setDefaults()`**

```go
	if cfg.OrderBook.Depth == 0 {
		cfg.OrderBook.Depth = 20
	}
	if cfg.OrderBook.WallThreshold == 0 {
		cfg.OrderBook.WallThreshold = 5.0
	}
	if cfg.OrderBook.Concurrency == 0 {
		cfg.OrderBook.Concurrency = 5
	}
	if cfg.Sentiment.Model == "" {
		cfg.Sentiment.Model = "deepseek-chat"
	}
	if cfg.Sentiment.MaxItemsPerTicker == 0 {
		cfg.Sentiment.MaxItemsPerTicker = 5
	}
	if cfg.Sentiment.MaxForumPosts == 0 {
		cfg.Sentiment.MaxForumPosts = 5
	}
	if cfg.VectorDB.EmbeddingModel == "" {
		cfg.VectorDB.EmbeddingModel = "openai/text-embedding-3-small"
	}
	if cfg.VectorDB.MaxSimilarPatterns == 0 {
		cfg.VectorDB.MaxSimilarPatterns = 5
	}
	if cfg.VectorDB.MinSimilarity == 0 {
		cfg.VectorDB.MinSimilarity = 0.7
	}
```

- [ ] **Step 3: Update `config.example.yaml`**

Add sections:

```yaml
# Order book metrics (L2 snapshot)
orderbook:
  enabled: true
  depth: 20
  wall_threshold: 5.0
  concurrency: 5

# Sentiment scoring (news + SmartLab forum → LLM)
sentiment:
  enabled: true
  model: "deepseek-chat"
  max_items_per_ticker: 5
  forum_enabled: true
  max_forum_posts: 5

# Vector pattern memory (SQLite + OpenRouter embeddings)
vectordb:
  enabled: false
  openrouter_api_key: "sk-or-your-key"
  embedding_model: "openai/text-embedding-3-small"
  max_similar_patterns: 5
  min_similarity: 0.7
```

- [ ] **Step 4: Build and commit**

```bash
CGO_ENABLED=1 go build ./...
git add internal/config/config.go config.example.yaml
git commit -m "feat: add config for order book, sentiment, and vector DB"
```

---

## Task 2: Storage — PatternEmbedding Model and Repository Methods

**Files:**
- Modify: `internal/storage/models.go`
- Modify: `internal/storage/database.go`
- Modify: `internal/storage/repository.go`

- [ ] **Step 1: Add PatternEmbedding model to `models.go`**

Add at end of file:

```go
type PatternEmbedding struct {
	ID            uint      `gorm:"primarykey" json:"id"`
	CreatedAt     time.Time `json:"created_at"`
	TradeID       uint      `gorm:"not null;index" json:"trade_id"`
	Ticker        string    `gorm:"not null;index" json:"ticker"`
	EntryFeatures string    `gorm:"type:text;not null" json:"entry_features"`
	Embedding     []byte    `gorm:"type:blob;not null" json:"-"`
	Outcome       string    `json:"outcome"`
	PnL           float64   `json:"pnl"`
	HoldHours     float64   `json:"hold_hours"`
}
```

- [ ] **Step 2: Register in AutoMigrate**

```go
if err := db.AutoMigrate(&Trade{}, &AnalysisLog{}, &PortfolioSnapshot{}, &TradeOutcome{}, &JournalLesson{}, &PatternEmbedding{}); err != nil {
```

- [ ] **Step 3: Add repository methods**

```go
func (r *Repository) SavePatternEmbedding(pe *PatternEmbedding) error {
	return r.db.Create(pe).Error
}

func (r *Repository) UpdatePatternOutcome(tradeID uint, outcome string, pnl float64, holdHours float64) error {
	return r.db.Model(&PatternEmbedding{}).Where("trade_id = ?", tradeID).
		Updates(map[string]interface{}{
			"outcome":    outcome,
			"pnl":        pnl,
			"hold_hours": holdHours,
		}).Error
}

func (r *Repository) GetAllClosedEmbeddings() ([]PatternEmbedding, error) {
	var patterns []PatternEmbedding
	err := r.db.Where("outcome != '' AND outcome IS NOT NULL").
		Find(&patterns).Error
	return patterns, err
}
```

- [ ] **Step 4: Build and commit**

```bash
CGO_ENABLED=1 go build ./...
git add internal/storage/
git commit -m "feat: add PatternEmbedding model and repository methods"
```

---

## Task 3: Order Book — Broker Method and Metrics

**Files:**
- Modify: `internal/broker/portfolio.go`
- Create: `internal/orderbook/metrics.go`
- Create: `internal/orderbook/metrics_test.go`

- [ ] **Step 1: Add `GetOrderBookFull` to `internal/broker/portfolio.go`**

Add types and method after existing `GetSpreadPct`:

```go
type OrderBookLevel struct {
	Price  float64
	Volume int64
}

type OrderBookRaw struct {
	Bids    []OrderBookLevel
	Asks    []OrderBookLevel
	BestBid float64
	BestAsk float64
}

func (bc *BrokerClient) GetOrderBookFull(instrumentUID string, depth int) (*OrderBookRaw, error) {
	md := bc.Client.NewMarketDataServiceClient()
	resp, err := md.GetOrderBook(instrumentUID, int32(depth))
	if err != nil {
		return nil, err
	}

	raw := &OrderBookRaw{}
	for _, b := range resp.GetBids() {
		raw.Bids = append(raw.Bids, OrderBookLevel{
			Price:  b.GetPrice().ToFloat(),
			Volume: b.GetQuantity(),
		})
	}
	for _, a := range resp.GetAsks() {
		raw.Asks = append(raw.Asks, OrderBookLevel{
			Price:  a.GetPrice().ToFloat(),
			Volume: a.GetQuantity(),
		})
	}
	if len(raw.Bids) > 0 {
		raw.BestBid = raw.Bids[0].Price
	}
	if len(raw.Asks) > 0 {
		raw.BestAsk = raw.Asks[0].Price
	}
	return raw, nil
}
```

- [ ] **Step 2: Write tests for metrics in `internal/orderbook/metrics_test.go`**

```go
package orderbook

import (
	"testing"

	"github.com/camuig/rus-trader/internal/broker"
)

func TestComputeMetrics_BuyPressure(t *testing.T) {
	book := &broker.OrderBookRaw{
		Bids: []broker.OrderBookLevel{
			{Price: 100, Volume: 500},
			{Price: 99, Volume: 300},
		},
		Asks: []broker.OrderBookLevel{
			{Price: 101, Volume: 200},
			{Price: 102, Volume: 100},
		},
		BestBid: 100, BestAsk: 101,
	}
	m := ComputeMetrics("TEST", book, 5.0)
	if m.BidAskImbalance < 2.0 {
		t.Errorf("expected imbalance > 2.0 (800/300), got %.2f", m.BidAskImbalance)
	}
	if m.SpreadPct < 0.9 || m.SpreadPct > 1.1 {
		t.Errorf("expected spread ~1%%, got %.2f%%", m.SpreadPct)
	}
}

func TestComputeMetrics_WallDetection(t *testing.T) {
	book := &broker.OrderBookRaw{
		Bids: []broker.OrderBookLevel{
			{Price: 100, Volume: 100},
			{Price: 99, Volume: 100},
			{Price: 98, Volume: 100},
			{Price: 97, Volume: 800}, // wall: 800 vs avg ~275
		},
		Asks: []broker.OrderBookLevel{
			{Price: 101, Volume: 50},
		},
		BestBid: 100, BestAsk: 101,
	}
	m := ComputeMetrics("TEST", book, 2.0) // lower threshold for test
	if m.BidWall == nil {
		t.Fatal("expected bid wall")
	}
	if m.BidWall.Price != 97 {
		t.Errorf("expected wall at 97, got %.2f", m.BidWall.Price)
	}
}

func TestComputeMetrics_EmptyBook(t *testing.T) {
	book := &broker.OrderBookRaw{}
	m := ComputeMetrics("TEST", book, 5.0)
	if m.BidAskImbalance != 0 {
		t.Errorf("expected 0 imbalance for empty book, got %.2f", m.BidAskImbalance)
	}
	if m.BidWall != nil || m.AskWall != nil {
		t.Error("expected no walls for empty book")
	}
}

func TestComputeMetrics_NilBook(t *testing.T) {
	m := ComputeMetrics("TEST", nil, 5.0)
	if m.BidAskImbalance != 0 {
		t.Errorf("expected 0 for nil book")
	}
}
```

- [ ] **Step 3: Implement `internal/orderbook/metrics.go`**

```go
package orderbook

import (
	"github.com/camuig/rus-trader/internal/broker"
)

type OrderBookMetrics struct {
	Ticker          string
	BidAskImbalance float64
	SpreadPct       float64
	BidWall         *Wall
	AskWall         *Wall
}

type Wall struct {
	Price  float64
	Volume int64
	Ratio  float64
}

func ComputeMetrics(ticker string, book *broker.OrderBookRaw, wallThreshold float64) OrderBookMetrics {
	m := OrderBookMetrics{Ticker: ticker}
	if book == nil {
		return m
	}

	// Spread
	if book.BestBid > 0 && book.BestAsk > 0 {
		m.SpreadPct = (book.BestAsk - book.BestBid) / book.BestBid * 100
	}

	// Bid/Ask imbalance
	var bidTotal, askTotal int64
	for _, b := range book.Bids {
		bidTotal += b.Volume
	}
	for _, a := range book.Asks {
		askTotal += a.Volume
	}
	if askTotal > 0 {
		m.BidAskImbalance = float64(bidTotal) / float64(askTotal)
	} else if bidTotal > 0 {
		m.BidAskImbalance = 10.0 // cap
	}

	// Wall detection
	m.BidWall = detectWall(book.Bids, wallThreshold)
	m.AskWall = detectWall(book.Asks, wallThreshold)

	return m
}

func detectWall(levels []broker.OrderBookLevel, threshold float64) *Wall {
	if len(levels) == 0 || threshold <= 0 {
		return nil
	}
	var totalVol int64
	for _, l := range levels {
		totalVol += l.Volume
	}
	avg := float64(totalVol) / float64(len(levels))
	if avg <= 0 {
		return nil
	}

	var best *Wall
	for _, l := range levels {
		ratio := float64(l.Volume) / avg
		if ratio >= threshold {
			if best == nil || l.Volume > best.Volume {
				best = &Wall{Price: l.Price, Volume: l.Volume, Ratio: ratio}
			}
		}
	}
	return best
}
```

- [ ] **Step 4: Run tests and commit**

```bash
CGO_ENABLED=1 go test ./internal/orderbook/ -v
git add internal/broker/portfolio.go internal/orderbook/
git commit -m "feat: add order book metrics (imbalance, spread, wall detection)"
```

---

## Task 4: Sentiment — SmartLab Forum Parser and LLM Scorer

**Files:**
- Create: `internal/sentiment/smartlab_forum.go`
- Create: `internal/sentiment/scorer.go`
- Create: `internal/sentiment/sentiment_test.go`

- [ ] **Step 1: Create SmartLab forum fixture**

Create `internal/sentiment/testdata/forum.html` with a minimal fixture mimicking SmartLab forum table (3-4 topic rows with titles and dates).

First check real structure:
```bash
curl -sL 'https://smart-lab.ru/forum/SBER/' -H 'User-Agent: Mozilla/5.0' | grep -A 5 '<tr' | head -40
```

Create fixture based on observed structure.

- [ ] **Step 2: Write tests in `internal/sentiment/sentiment_test.go`**

```go
package sentiment

import (
	"testing"
)

func TestLabelFromScore(t *testing.T) {
	tests := []struct {
		score float64
		label string
	}{
		{0.8, "very_positive"},
		{0.5, "positive"},
		{0.1, "neutral"},
		{-0.1, "neutral"},
		{-0.5, "negative"},
		{-0.8, "very_negative"},
	}
	for _, tt := range tests {
		got := LabelFromScore(tt.score)
		if got != tt.label {
			t.Errorf("LabelFromScore(%.1f) = %q, want %q", tt.score, got, tt.label)
		}
	}
}

func TestBuildSentimentPrompt(t *testing.T) {
	tickerTexts := map[string][]string{
		"SBER": {"Сбербанк повысил прогноз", "Рекордные дивиденды"},
		"GAZP": {"Газпром снизил добычу"},
	}
	prompt := BuildSentimentPrompt(tickerTexts)
	if len(prompt) == 0 {
		t.Fatal("expected non-empty prompt")
	}
	if !containsStr(prompt, "SBER") || !containsStr(prompt, "GAZP") {
		t.Errorf("prompt should mention both tickers: %s", prompt[:200])
	}
}

func containsStr(s, sub string) bool {
	return len(s) > 0 && len(sub) > 0 && (s == sub || len(s) > len(sub) && (s[:len(sub)] == sub || containsStr(s[1:], sub)))
}
```

Use `strings.Contains` for `containsStr` — replace with proper import.

- [ ] **Step 3: Implement `internal/sentiment/smartlab_forum.go`**

```go
package sentiment

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"golang.org/x/net/html"
)

type ForumPost struct {
	Title string
	Date  time.Time
}

func FetchForumPosts(ctx context.Context, ticker string, maxPosts int) ([]ForumPost, error) {
	url := fmt.Sprintf("https://smart-lab.ru/forum/%s/", ticker)
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 rus-trader/1.0")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	doc, err := html.Parse(resp.Body)
	if err != nil {
		return nil, err
	}

	posts := parseForumPosts(doc, maxPosts)
	return posts, nil
}

func parseForumPosts(doc *html.Node, maxPosts int) []ForumPost {
	var posts []ForumPost
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		// Look for topic links in forum table
		if n.Type == html.ElementNode && n.Data == "a" {
			for _, a := range n.Attr {
				if a.Key == "href" && strings.Contains(a.Val, "/blog/") {
					title := extractNodeText(n)
					title = strings.TrimSpace(title)
					if title != "" && len(posts) < maxPosts {
						posts = append(posts, ForumPost{Title: title})
					}
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)
	return posts
}

func extractNodeText(n *html.Node) string {
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
	return sb.String()
}
```

- [ ] **Step 4: Implement `internal/sentiment/scorer.go`**

```go
package sentiment

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/camuig/rus-trader/internal/ai"
	"github.com/camuig/rus-trader/internal/config"
	"github.com/camuig/rus-trader/internal/logger"
)

type SentimentResult struct {
	Ticker string  `json:"ticker"`
	Score  float64 `json:"score"`
	Label  string  `json:"-"`
	Reason string  `json:"reason"`
}

type Scorer struct {
	aiClient *ai.DeepSeekClient
	config   *config.Config
	logger   *logger.Logger
}

func NewScorer(aiClient *ai.DeepSeekClient, cfg *config.Config, log *logger.Logger) *Scorer {
	return &Scorer{aiClient: aiClient, config: cfg, logger: log}
}

const sentimentSystemPrompt = `Оцени sentiment по каждому тикеру на основе новостей и мнений трейдеров.
Шкала: -1.0 (крайне негативный) до +1.0 (крайне позитивный). 0 = нейтральный.
Формат: JSON массив.
[{"ticker":"SBER","score":0.6,"reason":"Рекордные дивиденды"}]
`

func (s *Scorer) ScoreBatch(ctx context.Context, tickerTexts map[string][]string) (map[string]*SentimentResult, error) {
	if len(tickerTexts) == 0 {
		return nil, nil
	}

	userPrompt := BuildSentimentPrompt(tickerTexts)

	model := s.config.Sentiment.Model
	if model == "" {
		model = "deepseek-chat"
	}

	rawResponse, err := s.aiClient.CallLLM(ctx, sentimentSystemPrompt, userPrompt, model, 60)
	if err != nil {
		return nil, fmt.Errorf("sentiment scoring: %w", err)
	}

	var results []SentimentResult
	cleaned := ai.StripThinkTags(rawResponse)
	if start := strings.Index(cleaned, "["); start >= 0 {
		if end := strings.LastIndex(cleaned, "]"); end > start {
			_ = json.Unmarshal([]byte(cleaned[start:end+1]), &results)
		}
	}

	resultMap := make(map[string]*SentimentResult, len(results))
	for i := range results {
		results[i].Label = LabelFromScore(results[i].Score)
		resultMap[results[i].Ticker] = &results[i]
	}
	return resultMap, nil
}

func BuildSentimentPrompt(tickerTexts map[string][]string) string {
	var sb strings.Builder
	sb.WriteString("Оцени sentiment по каждому тикеру:\n\n")
	for ticker, texts := range tickerTexts {
		sb.WriteString(fmt.Sprintf("%s:\n", ticker))
		for _, t := range texts {
			sb.WriteString(fmt.Sprintf("- %s\n", t))
		}
		sb.WriteString("\n")
	}
	return sb.String()
}

func LabelFromScore(score float64) string {
	switch {
	case score >= 0.6:
		return "very_positive"
	case score >= 0.3:
		return "positive"
	case score <= -0.6:
		return "very_negative"
	case score <= -0.3:
		return "negative"
	default:
		return "neutral"
	}
}
```

Note: `s.aiClient.CallLLM` needs the existing `callLLM` method to be exported. Alternatively, add a public wrapper. See Step 5.

- [ ] **Step 5: Export callLLM in `internal/ai/deepseek.go`**

The existing `callLLM` method is private (lowercase). Add a public wrapper:

```go
// CallLLM is the public version of callLLM for use by other packages.
func (d *DeepSeekClient) CallLLM(ctx context.Context, sysPrompt, userPrompt, model string, timeoutSec int) (string, error) {
	return d.callLLM(ctx, sysPrompt, userPrompt, model, timeoutSec)
}
```

- [ ] **Step 6: Run tests and commit**

```bash
CGO_ENABLED=1 go test ./internal/sentiment/ -v
git add internal/sentiment/ internal/ai/deepseek.go
git commit -m "feat: add sentiment scoring (SmartLab forum + LLM batch scorer)"
```

---

## Task 5: Vector DB — Embeddings, Cosine, Store

**Files:**
- Create: `internal/vectordb/cosine.go`
- Create: `internal/vectordb/embeddings.go`
- Create: `internal/vectordb/store.go`
- Create: `internal/vectordb/store_test.go`

- [ ] **Step 1: Write tests in `internal/vectordb/store_test.go`**

```go
package vectordb

import (
	"math"
	"testing"
)

func TestCosineSimilarity_Identical(t *testing.T) {
	a := []float32{1, 2, 3}
	sim := CosineSimilarity(a, a)
	if math.Abs(sim-1.0) > 0.001 {
		t.Errorf("identical vectors should have similarity 1.0, got %f", sim)
	}
}

func TestCosineSimilarity_Orthogonal(t *testing.T) {
	a := []float32{1, 0, 0}
	b := []float32{0, 1, 0}
	sim := CosineSimilarity(a, b)
	if math.Abs(sim) > 0.001 {
		t.Errorf("orthogonal vectors should have similarity 0, got %f", sim)
	}
}

func TestCosineSimilarity_Opposite(t *testing.T) {
	a := []float32{1, 2, 3}
	b := []float32{-1, -2, -3}
	sim := CosineSimilarity(a, b)
	if math.Abs(sim+1.0) > 0.001 {
		t.Errorf("opposite vectors should have similarity -1.0, got %f", sim)
	}
}

func TestSerializeDeserialize(t *testing.T) {
	original := []float32{1.5, -2.3, 0.0, 100.123}
	data := SerializeEmbedding(original)
	restored := DeserializeEmbedding(data)

	if len(restored) != len(original) {
		t.Fatalf("length mismatch: %d vs %d", len(restored), len(original))
	}
	for i := range original {
		if math.Abs(float64(original[i]-restored[i])) > 0.0001 {
			t.Errorf("value mismatch at %d: %f vs %f", i, original[i], restored[i])
		}
	}
}

func TestSerializeEmpty(t *testing.T) {
	data := SerializeEmbedding(nil)
	restored := DeserializeEmbedding(data)
	if len(restored) != 0 {
		t.Errorf("expected empty, got %d", len(restored))
	}
}
```

- [ ] **Step 2: Implement `internal/vectordb/cosine.go`**

```go
package vectordb

import (
	"encoding/binary"
	"math"
)

func CosineSimilarity(a, b []float32) float64 {
	if len(a) != len(b) || len(a) == 0 {
		return 0
	}
	var dot, normA, normB float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
		normA += float64(a[i]) * float64(a[i])
		normB += float64(b[i]) * float64(b[i])
	}
	denom := math.Sqrt(normA) * math.Sqrt(normB)
	if denom == 0 {
		return 0
	}
	return dot / denom
}

func SerializeEmbedding(v []float32) []byte {
	buf := make([]byte, len(v)*4)
	for i, f := range v {
		binary.LittleEndian.PutUint32(buf[i*4:], math.Float32bits(f))
	}
	return buf
}

func DeserializeEmbedding(b []byte) []float32 {
	if len(b) == 0 || len(b)%4 != 0 {
		return nil
	}
	v := make([]float32, len(b)/4)
	for i := range v {
		v[i] = math.Float32frombits(binary.LittleEndian.Uint32(b[i*4:]))
	}
	return v
}
```

- [ ] **Step 3: Implement `internal/vectordb/embeddings.go`**

```go
package vectordb

import (
	"context"
	"fmt"

	openai "github.com/sashabaranov/go-openai"

	"github.com/camuig/rus-trader/internal/logger"
)

type EmbeddingClient struct {
	client *openai.Client
	model  openai.EmbeddingModel
	logger *logger.Logger
}

func NewEmbeddingClient(apiKey, model string, log *logger.Logger) *EmbeddingClient {
	cfg := openai.DefaultConfig(apiKey)
	cfg.BaseURL = "https://openrouter.ai/api/v1"
	return &EmbeddingClient{
		client: openai.NewClientWithConfig(cfg),
		model:  openai.EmbeddingModel(model),
		logger: log,
	}
}

func (c *EmbeddingClient) Embed(ctx context.Context, text string) ([]float32, error) {
	resp, err := c.client.CreateEmbeddings(ctx, openai.EmbeddingRequest{
		Input: []string{text},
		Model: c.model,
	})
	if err != nil {
		return nil, fmt.Errorf("openrouter embedding: %w", err)
	}
	if len(resp.Data) == 0 {
		return nil, fmt.Errorf("openrouter returned empty embedding")
	}
	return resp.Data[0].Embedding, nil
}
```

- [ ] **Step 4: Implement `internal/vectordb/store.go`**

```go
package vectordb

import (
	"context"
	"sort"
	"sync"

	"github.com/camuig/rus-trader/internal/logger"
	"github.com/camuig/rus-trader/internal/storage"
)

type PatternMatch struct {
	Ticker        string
	EntryFeatures string
	Outcome       string
	PnL           float64
	HoldHours     float64
	Similarity    float64
}

type cachedPattern struct {
	embedding []float32
	ticker    string
	features  string
	outcome   string
	pnl       float64
	holdHours float64
}

type Store struct {
	repo     *storage.Repository
	embedder *EmbeddingClient
	logger   *logger.Logger
	patterns []cachedPattern
	mu       sync.RWMutex
}

func NewStore(repo *storage.Repository, embedder *EmbeddingClient, log *logger.Logger) *Store {
	return &Store{repo: repo, embedder: embedder, logger: log}
}

func (s *Store) LoadAll() error {
	records, err := s.repo.GetAllClosedEmbeddings()
	if err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	s.patterns = make([]cachedPattern, 0, len(records))
	for _, r := range records {
		emb := DeserializeEmbedding(r.Embedding)
		if len(emb) == 0 {
			continue
		}
		s.patterns = append(s.patterns, cachedPattern{
			embedding: emb,
			ticker:    r.Ticker,
			features:  r.EntryFeatures,
			outcome:   r.Outcome,
			pnl:       r.PnL,
			holdHours: r.HoldHours,
		})
	}

	if s.logger != nil {
		s.logger.Info("vector store loaded", "patterns", len(s.patterns))
	}
	return nil
}

func (s *Store) SaveEntry(ctx context.Context, tradeID uint, ticker, features string) error {
	emb, err := s.embedder.Embed(ctx, features)
	if err != nil {
		return err
	}

	pe := &storage.PatternEmbedding{
		TradeID:       tradeID,
		Ticker:        ticker,
		EntryFeatures: features,
		Embedding:     SerializeEmbedding(emb),
	}
	if err := s.repo.SavePatternEmbedding(pe); err != nil {
		return err
	}

	// Add to in-memory cache (outcome empty for now)
	s.mu.Lock()
	s.patterns = append(s.patterns, cachedPattern{
		embedding: emb,
		ticker:    ticker,
		features:  features,
	})
	s.mu.Unlock()

	return nil
}

func (s *Store) UpdateOutcome(tradeID uint, outcome string, pnl, holdHours float64) error {
	if err := s.repo.UpdatePatternOutcome(tradeID, outcome, pnl, holdHours); err != nil {
		return err
	}

	// Update in-memory cache — find by features match (tradeID not stored in cache)
	s.mu.Lock()
	for i := range s.patterns {
		if s.patterns[i].outcome == "" {
			s.patterns[i].outcome = outcome
			s.patterns[i].pnl = pnl
			s.patterns[i].holdHours = holdHours
			break // update first open pattern
		}
	}
	s.mu.Unlock()

	return nil
}

func (s *Store) FindSimilar(ctx context.Context, features string, maxResults int, minSimilarity float64) ([]PatternMatch, error) {
	queryEmb, err := s.embedder.Embed(ctx, features)
	if err != nil {
		return nil, err
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	var matches []PatternMatch
	for _, p := range s.patterns {
		if p.outcome == "" {
			continue // skip still-open positions
		}
		sim := CosineSimilarity(queryEmb, p.embedding)
		if sim >= minSimilarity {
			matches = append(matches, PatternMatch{
				Ticker:        p.ticker,
				EntryFeatures: p.features,
				Outcome:       p.outcome,
				PnL:           p.pnl,
				HoldHours:     p.holdHours,
				Similarity:    sim,
			})
		}
	}

	sort.Slice(matches, func(i, j int) bool {
		return matches[i].Similarity > matches[j].Similarity
	})

	if len(matches) > maxResults {
		matches = matches[:maxResults]
	}
	return matches, nil
}
```

- [ ] **Step 5: Run tests and commit**

```bash
CGO_ENABLED=1 go test ./internal/vectordb/ -v
git add internal/vectordb/
git commit -m "feat: add vector pattern memory (cosine, OpenRouter embeddings, SQLite store)"
```

---

## Task 6: Features Builder — Add Order Book and Sentiment

**Files:**
- Modify: `internal/features/builder.go`
- Modify: `internal/features/builder_test.go`

- [ ] **Step 1: Update `BuildTickerFeatures` signature**

Change from:
```go
func BuildTickerFeatures(snap broker.CandleSnapshot, div *dividends.DividendInfo) TickerFeature
```
To:
```go
func BuildTickerFeatures(snap broker.CandleSnapshot, div *dividends.DividendInfo, ob *orderbook.OrderBookMetrics, sent *sentiment.SentimentResult) TickerFeature
```

Add imports for `orderbook` and `sentiment` packages.

- [ ] **Step 2: Add order book feature text**

Add after dividend section, before building final summary:

```go
	// Order book
	if ob != nil {
		var obParts []string
		switch {
		case ob.BidAskImbalance >= 1.5:
			obParts = append(obParts, fmt.Sprintf("bid давление %.1fx", ob.BidAskImbalance))
		case ob.BidAskImbalance > 0 && ob.BidAskImbalance <= 0.7:
			obParts = append(obParts, fmt.Sprintf("ask давление (imb %.1f)", ob.BidAskImbalance))
		default:
			obParts = append(obParts, "нейтральный")
		}
		obParts = append(obParts, fmt.Sprintf("спред %.2f%%", ob.SpreadPct))
		if ob.BidWall != nil {
			obParts = append(obParts, fmt.Sprintf("bid wall %.2f (%.1fx)", ob.BidWall.Price, ob.BidWall.Ratio))
		}
		if ob.AskWall != nil {
			obParts = append(obParts, fmt.Sprintf("ask wall %.2f (%.1fx)", ob.AskWall.Price, ob.AskWall.Ratio))
		}
		parts = append(parts, "стакан: "+strings.Join(obParts, ", "))
	}
```

- [ ] **Step 3: Add sentiment feature text**

```go
	// Sentiment
	if sent != nil {
		switch {
		case sent.Score >= 0.3:
			parts = append(parts, fmt.Sprintf("sentiment %+.1f (%s)", sent.Score, sent.Reason))
		case sent.Score <= -0.3:
			parts = append(parts, fmt.Sprintf("sentiment %+.1f (%s)", sent.Score, sent.Reason))
		}
		// neutral: omit from features to save space
	}
```

- [ ] **Step 4: Update existing tests — fix calls to BuildTickerFeatures**

All existing test calls need two new nil params:
```go
// Before:
f := BuildTickerFeatures(s, nil)
// After:
f := BuildTickerFeatures(s, nil, nil, nil)
```

Add new tests:

```go
func TestBuildFeatures_WithOrderBook(t *testing.T) {
	s := snap("TEST", 100, indicators.Indicators{RSI14: 55, EMA9: 101, EMA21: 99, ATR14: 2.0, RelVolume: 1.0})
	ob := &orderbook.OrderBookMetrics{
		Ticker: "TEST", BidAskImbalance: 2.0, SpreadPct: 0.15,
		BidWall: &orderbook.Wall{Price: 98.0, Volume: 5000, Ratio: 6.2},
	}
	f := BuildTickerFeatures(s, nil, ob, nil)
	if !containsAny(f.Summary, "bid давление", "стакан") {
		t.Errorf("expected order book mention: %s", f.Summary)
	}
	if !containsAny(f.Summary, "wall") {
		t.Errorf("expected wall mention: %s", f.Summary)
	}
}

func TestBuildFeatures_WithSentiment(t *testing.T) {
	s := snap("TEST", 100, indicators.Indicators{RSI14: 55, EMA9: 101, EMA21: 99, ATR14: 2.0, RelVolume: 1.0})
	sent := &sentiment.SentimentResult{Ticker: "TEST", Score: 0.7, Label: "positive", Reason: "рекордные дивиденды"}
	f := BuildTickerFeatures(s, nil, nil, sent)
	if !containsAny(f.Summary, "sentiment", "+0.7") {
		t.Errorf("expected sentiment mention: %s", f.Summary)
	}
}
```

Add imports for `orderbook` and `sentiment` packages in test file.

- [ ] **Step 5: Update scheduler's existing call to BuildTickerFeatures**

In `internal/scheduler/scheduler.go`, find the line:
```go
tf := features.BuildTickerFeatures(snap, divPtr)
```
Change to:
```go
tf := features.BuildTickerFeatures(snap, divPtr, nil, nil)
```

This keeps the build green between tasks. Task 9 will replace these nils with real data.

Also add import `"github.com/camuig/rus-trader/internal/orderbook"` and `"github.com/camuig/rus-trader/internal/sentiment"` to scheduler.go (needed for the nil-typed params to compile — or just use untyped nil which doesn't need imports since the params are pointer types).

- [ ] **Step 6: Run tests and commit**

```bash
CGO_ENABLED=1 go test ./internal/features/ -v
CGO_ENABLED=1 go build ./...
git add internal/features/ internal/scheduler/scheduler.go
git commit -m "feat: add order book and sentiment to textual features"
```

---

## Task 7: Screening Prompt — Add Similar Patterns Section

**Files:**
- Modify: `internal/ai/types.go`
- Modify: `internal/ai/prompt_screening.go`

- [ ] **Step 1: Add SimilarPatterns field to ScreeningRequest**

In `internal/ai/types.go`, add field to ScreeningRequest:

```go
type ScreeningRequest struct {
	TickerFeatures  []string
	Market          MarketContext
	GlobalNews      []string
	TickerNews      map[string][]string
	Lessons         []string
	TodayTraded     []string
	Stats           PerformanceStats
	CurrentTime     time.Time
	AvailableRub    float64
	SimilarPatterns map[string][]PatternMatchInfo // NEW: ticker → similar historical patterns
}

// PatternMatchInfo is a simplified pattern match for the prompt.
type PatternMatchInfo struct {
	Features   string
	Outcome    string
	PnL        float64
	HoldHours  float64
	Similarity float64
}
```

- [ ] **Step 2: Add similar patterns section to `BuildScreeningPrompt`**

In `prompt_screening.go`, add after ticker features section and before ticker news:

```go
	// Similar patterns from vector memory
	if len(req.SimilarPatterns) > 0 {
		sb.WriteString("## Похожие исторические паттерны\n")
		for ticker, patterns := range req.SimilarPatterns {
			if len(patterns) == 0 {
				continue
			}
			var wins, losses int
			var winPnL, lossPnL float64
			for _, p := range patterns {
				if p.Outcome == "win" {
					wins++
					winPnL += p.PnL
				} else {
					losses++
					lossPnL += p.PnL
				}
			}
			total := wins + losses
			wr := 0.0
			if total > 0 {
				wr = float64(wins) / float64(total) * 100
			}
			sb.WriteString(fmt.Sprintf("%s: %d похожих — %d win", ticker, total, wins))
			if wins > 0 {
				sb.WriteString(fmt.Sprintf(" (+avg %.0f₽)", winPnL/float64(wins)))
			}
			sb.WriteString(fmt.Sprintf(", %d loss", losses))
			if losses > 0 {
				sb.WriteString(fmt.Sprintf(" (avg %.0f₽)", lossPnL/float64(losses)))
			}
			sb.WriteString(fmt.Sprintf(". WR %.0f%%.\n", wr))
			// Show top 2 closest
			for i, p := range patterns {
				if i >= 2 {
					break
				}
				sb.WriteString(fmt.Sprintf("  (%.2f) %s → %s %+.0f₽ за %.0fч\n",
					p.Similarity, truncateStr(p.Features, 60), p.Outcome, p.PnL, p.HoldHours))
			}
		}
		sb.WriteString("\n")
	}
```

Add helper if not exists:
```go
func truncateStr(s string, maxRunes int) string {
	r := []rune(s)
	if len(r) <= maxRunes {
		return s
	}
	return string(r[:maxRunes-1]) + "…"
}
```

- [ ] **Step 3: Build and commit**

```bash
CGO_ENABLED=1 go build ./...
git add internal/ai/types.go internal/ai/prompt_screening.go
git commit -m "feat: add similar patterns section to screening prompt"
```

---

## Task 8: Executor — Save and Update Embeddings

**Files:**
- Modify: `internal/executor/executor.go`

- [ ] **Step 1: Add vectorStore field to Executor**

```go
type Executor struct {
	broker     *broker.BrokerClient
	repo       *storage.Repository
	notifier   *telegram.Notifier
	config     *config.Config
	logger     *logger.Logger
	indicators map[string]indicators.Indicators
	journal    *journal.Journal
	features   map[string]string
	vectorStore interface {  // avoid importing vectordb to prevent cycles
		SaveEntry(ctx context.Context, tradeID uint, ticker, features string) error
		UpdateOutcome(tradeID uint, outcome string, pnl, holdHours float64) error
	}
}

func (e *Executor) SetVectorStore(vs interface {
	SaveEntry(ctx context.Context, tradeID uint, ticker, features string) error
	UpdateOutcome(tradeID uint, outcome string, pnl, holdHours float64) error
}) {
	e.vectorStore = vs
}
```

Add `"context"` to imports.

- [ ] **Step 2: Save embedding on BUY**

In `executeBuy`, after `e.repo.SaveTrade(trade)` succeeds, add:

```go
	// Save embedding for vector pattern memory
	if e.vectorStore != nil && trade.EntryFeatures != "" {
		go func(id uint, ticker, features string) {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			if err := e.vectorStore.SaveEntry(ctx, id, ticker, features); err != nil {
				e.logger.Error("save pattern embedding", "ticker", ticker, "error", err)
			}
		}(trade.ID, trade.Ticker, trade.EntryFeatures)
	}
```

Add `"time"` to imports if not present.

- [ ] **Step 3: Update embedding on SELL**

In `executeSell`, after `e.journal.RecordOutcome(...)`, add:

```go
	// Update vector pattern memory with outcome
	if e.vectorStore != nil {
		outcome := "loss"
		if pnl > 0 {
			outcome = "win"
		} else if pnl == 0 {
			outcome = "breakeven"
		}
		holdHours := time.Since(openTrade.CreatedAt).Hours()
		if err := e.vectorStore.UpdateOutcome(openTrade.ID, outcome, pnl, holdHours); err != nil {
			e.logger.Error("update pattern outcome", "ticker", d.Ticker, "error", err)
		}
	}
```

- [ ] **Step 4: Build and commit**

```bash
CGO_ENABLED=1 go build ./...
git add internal/executor/executor.go
git commit -m "feat: executor saves/updates pattern embeddings on BUY/SELL"
```

---

## Task 9: Scheduler — Full Integration

**Files:**
- Modify: `internal/scheduler/scheduler.go`
- Modify: `cmd/bot/main.go`

This is the largest task — wires order book, sentiment, and vector DB into the pipeline.

- [ ] **Step 1: Add new dependencies to Scheduler**

Add fields and update NewScheduler:

```go
type Scheduler struct {
	// ... existing fields ...
	sentiment  *sentiment.Scorer
	vectorStore *vectordb.Store
}
```

Update NewScheduler to accept `sentScorer *sentiment.Scorer, vs *vectordb.Store` (both can be nil).

Add imports: `"github.com/camuig/rus-trader/internal/orderbook"`, `"github.com/camuig/rus-trader/internal/sentiment"`, `"github.com/camuig/rus-trader/internal/vectordb"`.

- [ ] **Step 2: Add `fetchOrderBooks` method**

```go
func (s *Scheduler) fetchOrderBooks(ctx context.Context, snapshots []broker.CandleSnapshot) map[string]*orderbook.OrderBookMetrics {
	if !s.config.OrderBook.Enabled {
		return nil
	}

	result := make(map[string]*orderbook.OrderBookMetrics, len(snapshots))
	var mu sync.Mutex
	sem := make(chan struct{}, s.config.OrderBook.Concurrency)

	var wg sync.WaitGroup
	for _, snap := range snapshots {
		snap := snap
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			uid, err := s.broker.ResolveTickerToUID(snap.Ticker)
			if err != nil {
				return
			}
			book, err := s.broker.GetOrderBookFull(uid, s.config.OrderBook.Depth)
			if err != nil {
				s.logger.Debug("order book fetch failed", "ticker", snap.Ticker, "error", err)
				return
			}
			metrics := orderbook.ComputeMetrics(snap.Ticker, book, s.config.OrderBook.WallThreshold)
			mu.Lock()
			result[snap.Ticker] = &metrics
			mu.Unlock()
		}()
	}
	wg.Wait()

	s.logger.Info("order book fetched", "count", len(result))
	return result
}
```

Add `"sync"` back to imports.

- [ ] **Step 3: Add `scoreSentiment` method**

```go
func (s *Scheduler) scoreSentiment(ctx context.Context, tickerNews map[string][]moex.NewsItem, globalNews []string, tickers []string) map[string]*sentiment.SentimentResult {
	if s.sentiment == nil || !s.config.Sentiment.Enabled {
		return nil
	}

	// Build per-ticker text corpus
	tickerTexts := make(map[string][]string)
	maxItems := s.config.Sentiment.MaxItemsPerTicker

	for _, ticker := range tickers {
		var texts []string
		if items, ok := tickerNews[ticker]; ok {
			for _, n := range items {
				if len(texts) >= maxItems {
					break
				}
				texts = append(texts, n.Title)
			}
		}
		// Add SmartLab forum posts if enabled
		if s.config.Sentiment.ForumEnabled {
			forumCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			posts, err := sentiment.FetchForumPosts(forumCtx, ticker, s.config.Sentiment.MaxForumPosts)
			cancel()
			if err == nil {
				for _, p := range posts {
					if len(texts) >= maxItems {
						break
					}
					texts = append(texts, p.Title)
				}
			}
		}
		if len(texts) > 0 {
			tickerTexts[ticker] = texts
		}
	}

	if len(tickerTexts) == 0 {
		return nil
	}

	results, err := s.sentiment.ScoreBatch(ctx, tickerTexts)
	if err != nil {
		s.logger.Error("sentiment scoring failed", "error", err)
		return nil
	}

	s.logger.Info("sentiment scored", "tickers", len(results))
	return results
}
```

- [ ] **Step 4: Add `findSimilarPatterns` method**

```go
func (s *Scheduler) findSimilarPatterns(ctx context.Context, features map[string]string) map[string][]ai.PatternMatchInfo {
	if s.vectorStore == nil || !s.config.VectorDB.Enabled {
		return nil
	}

	result := make(map[string][]ai.PatternMatchInfo)
	for ticker, feat := range features {
		matches, err := s.vectorStore.FindSimilar(ctx, feat,
			s.config.VectorDB.MaxSimilarPatterns, s.config.VectorDB.MinSimilarity)
		if err != nil {
			s.logger.Debug("vector search failed", "ticker", ticker, "error", err)
			continue
		}
		if len(matches) == 0 {
			continue
		}
		var infos []ai.PatternMatchInfo
		for _, m := range matches {
			infos = append(infos, ai.PatternMatchInfo{
				Features:   m.EntryFeatures,
				Outcome:    m.Outcome,
				PnL:        m.PnL,
				HoldHours:  m.HoldHours,
				Similarity: m.Similarity,
			})
		}
		result[ticker] = infos
	}

	if len(result) > 0 {
		s.logger.Info("similar patterns found", "tickers_with_matches", len(result))
	}
	return result
}
```

- [ ] **Step 5: Insert into runCycle flow**

In runCycle, after features are built (`s.executor.SetFeatures(featuresMap)`) and before Screening Agent call:

```go
	// Fetch order book metrics (parallel)
	obMetrics := s.fetchOrderBooks(ctx, snapshots)

	// Score sentiment (LLM, non-fatal)
	var screenerTickers []string
	for _, snap := range snapshots {
		screenerTickers = append(screenerTickers, snap.Ticker)
	}
	sentimentMap := s.scoreSentiment(ctx, tickerNews, globalNews, screenerTickers)

	// Rebuild features with order book + sentiment
	featuresMap = make(map[string]string, len(snapshots))
	var tickerFeaturesList []string
	for _, snap := range snapshots {
		var divPtr *dividends.DividendInfo
		if d, ok := divMap[snap.Ticker]; ok {
			divPtr = &d
		}
		var obPtr *orderbook.OrderBookMetrics
		if ob, ok := obMetrics[snap.Ticker]; ok {
			obPtr = ob
		}
		var sentPtr *sentiment.SentimentResult
		if s, ok := sentimentMap[snap.Ticker]; ok {
			sentPtr = s
		}
		tf := features.BuildTickerFeatures(snap, divPtr, obPtr, sentPtr)
		featuresMap[tf.Ticker] = tf.Summary
		tickerFeaturesList = append(tickerFeaturesList, tf.Summary)
	}
	s.executor.SetFeatures(featuresMap)

	// Find similar historical patterns (vector search)
	similarPatterns := s.findSimilarPatterns(ctx, featuresMap)
```

Then update the ScreeningRequest to include `SimilarPatterns: similarPatterns`.

NOTE: This replaces the existing feature-building block. The old block that called `features.BuildTickerFeatures(snap, divPtr)` should be replaced with the new one that passes all 4 params.

- [ ] **Step 6: Update `cmd/bot/main.go`**

Add initialization after journal setup:

```go
	// Sentiment scorer (optional)
	var sentScorer *sentiment.Scorer
	if cfg.Sentiment.Enabled {
		sentScorer = sentiment.NewScorer(aiClient, cfg, log)
	}

	// Vector pattern store (optional)
	var vs *vectordb.Store
	if cfg.VectorDB.Enabled && cfg.VectorDB.OpenRouterAPIKey != "" {
		embedder := vectordb.NewEmbeddingClient(cfg.VectorDB.OpenRouterAPIKey, cfg.VectorDB.EmbeddingModel, log)
		vs = vectordb.NewStore(repo, embedder, log)
		if err := vs.LoadAll(); err != nil {
			log.Error("vector store load failed", "error", err)
		}
		exec.SetVectorStore(vs)
	}
```

Update NewScheduler call with the two new params.

Add imports: `"github.com/camuig/rus-trader/internal/sentiment"`, `"github.com/camuig/rus-trader/internal/vectordb"`.

- [ ] **Step 7: Build, test, commit**

```bash
CGO_ENABLED=1 go build ./...
CGO_ENABLED=1 go test ./...
git add internal/scheduler/ cmd/bot/main.go
git commit -m "feat: integrate order book, sentiment, and vector DB into scheduler"
```

---

## Task 10: Config Example and README

**Files:**
- Modify: `config.example.yaml` (verify new sections from Task 1)
- Modify: `README.md`

- [ ] **Step 1: Update README.md with Phase 2 features**

Add section after Phase 1 docs:

```markdown
## Phase 2: Data Enrichment

### Order Book Metrics
Бот запрашивает стакан L2 (20 уровней) для каждого screened тикера через T-Invest `GetOrderBook`. Считает три метрики:
- **Bid/Ask Imbalance** — соотношение bid/ask объёмов. >1.5 = давление покупателей.
- **Spread** — bid/ask спред в процентах.
- **Wall Detection** — крупные уровни (≥5x среднего) — "стенки" в стакане.

Метрики добавляются в текстовые features: `"стакан: bid давление 1.8x, спред 0.12%, bid wall 298.50 (7.2x)"`.

### Sentiment Scoring
Существующие новости (Finam, world) + посты с форума SmartLab прогоняются через LLM (дешёвая модель `deepseek-chat`) для оценки sentiment по каждому тикеру: от -1.0 (крайне негативный) до +1.0 (крайне позитивный).

Результат включается в features: `"sentiment +0.6 (рекордные дивиденды)"`.

### Vector Pattern Memory
При каждой сделке сохраняется embedding текстовых features через OpenRouter (`text-embedding-3-small`). При screening — для каждого кандидата ищутся исторически похожие паттерны и их результаты.

В промпт Screening Agent передаётся: `"SBER: 5 похожих — 3 win (+avg 2.8%), 2 loss. WR 60%."`.

Конфигурация:
\```yaml
orderbook:
  enabled: true
  depth: 20
  wall_threshold: 5.0

sentiment:
  enabled: true
  model: "deepseek-chat"

vectordb:
  enabled: false  # requires OpenRouter API key
  openrouter_api_key: "sk-or-..."
  embedding_model: "openai/text-embedding-3-small"
\```
```

(Remove the backslash before triple backticks — escaping for nesting.)

- [ ] **Step 2: Commit**

```bash
git add README.md config.example.yaml
git commit -m "docs: update README and config for Phase 2 data enrichment"
```

---

## Verification Checklist

After all tasks:

- [ ] `CGO_ENABLED=1 go build ./...` — clean build
- [ ] `CGO_ENABLED=1 go test ./...` — all tests pass
- [ ] `CGO_ENABLED=1 go vet ./...` — no warnings
- [ ] Start bot with `orderbook.enabled: true`, `sentiment.enabled: true`, `vectordb.enabled: false` and verify:
  - Order book metrics in logs: `"order book fetched"`
  - Sentiment scoring in logs: `"sentiment scored"`
  - Features include order book and sentiment lines
  - Both AI agents still work correctly
- [ ] Enable `vectordb` with an OpenRouter key and verify:
  - `"vector store loaded"` at startup
  - Embeddings saved on BUY (check `pattern_embeddings` table)
  - Similar patterns shown in screening prompt
