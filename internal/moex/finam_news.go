package moex

import (
	"context"
	"fmt"
	"sort"
	"time"
)

const finamRSSURL = "https://www.finam.ru/analysis/conews/rsspoint/"

// FetchFinamNews fetches recent company news from Finam RSS feed.
func (c *Client) FetchFinamNews(ctx context.Context, maxItems int) ([]NewsItem, error) {
	if maxItems <= 0 {
		return nil, nil
	}

	items, err := c.fetchFeed(ctx, "Финам", finamRSSURL)
	if err != nil {
		return nil, fmt.Errorf("finam news: %w", err)
	}

	cutoff := time.Now().Add(-48 * time.Hour)
	filtered := make([]NewsItem, 0, len(items))
	for _, item := range items {
		item.Title = compactSpaces(item.Title)
		if item.Title == "" {
			continue
		}
		if !item.Published.IsZero() && item.Published.Before(cutoff) {
			continue
		}
		item.ID = hashNewsID(item.Source, item.Title, item.Published)
		filtered = append(filtered, item)
	}

	unique := dedupeNewsByTitle(filtered)
	sort.Slice(unique, func(i, j int) bool {
		return unique[i].Published.After(unique[j].Published)
	})
	if len(unique) > maxItems {
		unique = unique[:maxItems]
	}

	return unique, nil
}
