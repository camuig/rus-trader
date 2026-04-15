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

	s.mu.Lock()
	for i := range s.patterns {
		if s.patterns[i].outcome == "" {
			s.patterns[i].outcome = outcome
			s.patterns[i].pnl = pnl
			s.patterns[i].holdHours = holdHours
			break
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
			continue
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
