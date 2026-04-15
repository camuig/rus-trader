package scheduler

import (
	"context"
	"sync"
	"time"

	"github.com/camuig/rus-trader/internal/ai"
	"github.com/camuig/rus-trader/internal/broker"
	"github.com/camuig/rus-trader/internal/moex"
	"github.com/camuig/rus-trader/internal/orderbook"
	"github.com/camuig/rus-trader/internal/sentiment"
)

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

func (s *Scheduler) scoreSentiment(ctx context.Context, tickerNews map[string][]moex.NewsItem, tickers []string) map[string]*sentiment.SentimentResult {
	if s.sentiment == nil || !s.config.Sentiment.Enabled {
		return nil
	}

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

func (s *Scheduler) fetchPerformanceStats() ai.PerformanceStats {
	stats, err := s.repo.GetPerformanceStats7d()
	if err != nil {
		s.logger.Error("fetch performance stats", "error", err)
		return ai.PerformanceStats{}
	}

	losingStreak, err := s.repo.GetLosingStreak()
	if err != nil {
		s.logger.Error("fetch losing streak", "error", err)
	}

	return ai.PerformanceStats{
		WinRate7d:    stats.WinRate,
		AvgProfit:    stats.AvgProfit,
		AvgLoss:      stats.AvgLoss,
		TotalPnL7d:   stats.TotalPnL,
		TradeCount7d: stats.TradeCount,
		WorstTickers: stats.WorstTickers,
		LosingStreak: losingStreak,
	}
}
