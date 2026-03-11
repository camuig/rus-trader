package moex

import (
	"context"
	"encoding/xml"
	"fmt"
	"hash/fnv"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"
)

var worldNewsFeeds = []struct {
	name string
	url  string
}{
	{name: "BBC World", url: "https://feeds.bbci.co.uk/news/world/rss.xml"},
	{name: "NYT World", url: "https://rss.nytimes.com/services/xml/rss/nyt/World.xml"},
	{name: "WSJ World", url: "https://feeds.a.dj.com/rss/RSSWorldNews.xml"},
	{name: "Guardian World", url: "https://www.theguardian.com/world/rss"},
	{name: "Al Jazeera", url: "https://www.aljazeera.com/xml/rss/all.xml"},
}

type rssFeed struct {
	Channel struct {
		Items []rssItem `xml:"item"`
	} `xml:"channel"`
}

type rssItem struct {
	Title     string `xml:"title"`
	PubDate   string `xml:"pubDate"`
	Published string `xml:"published"`
	Updated   string `xml:"updated"`
}

type atomFeed struct {
	Entries []atomEntry `xml:"entry"`
}

type atomEntry struct {
	Title     string `xml:"title"`
	Updated   string `xml:"updated"`
	Published string `xml:"published"`
}

// FetchWorldNews returns compact world-market headlines for prompt context.
func (c *Client) FetchWorldNews(ctx context.Context, maxItems int) ([]NewsItem, error) {
	if maxItems <= 0 {
		return nil, nil
	}

	cutoff := time.Now().Add(-24 * time.Hour)
	collected := make([]NewsItem, 0, maxItems*2)
	errs := make([]string, 0, len(worldNewsFeeds))

	for _, feed := range worldNewsFeeds {
		items, err := c.fetchFeed(ctx, feed.name, feed.url)
		if err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", feed.name, err))
			continue
		}
		for _, item := range items {
			if !item.Published.IsZero() && item.Published.Before(cutoff) {
				continue
			}
			item.Title = compactSpaces(item.Title)
			if item.Title == "" {
				continue
			}
			item.ID = hashNewsID(item.Source, item.Title, item.Published)
			collected = append(collected, item)
		}
	}

	if len(collected) == 0 {
		if len(errs) == 0 {
			return nil, nil
		}
		return nil, fmt.Errorf("world news unavailable: %s", strings.Join(errs, "; "))
	}

	unique := dedupeNewsByTitle(collected)
	sort.Slice(unique, func(i, j int) bool {
		return unique[i].Published.After(unique[j].Published)
	})
	if len(unique) > maxItems {
		unique = unique[:maxItems]
	}

	return unique, nil
}

func (c *Client) fetchFeed(ctx context.Context, source, url string) ([]NewsItem, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch feed: %w", err)
	}
	body, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		return nil, fmt.Errorf("read feed body: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("feed status %d", resp.StatusCode)
	}

	items, err := parseNewsFeed(body, source)
	if err != nil {
		return nil, fmt.Errorf("parse feed: %w", err)
	}
	return items, nil
}

func parseNewsFeed(body []byte, source string) ([]NewsItem, error) {
	var rss rssFeed
	if err := xml.Unmarshal(body, &rss); err == nil && len(rss.Channel.Items) > 0 {
		items := make([]NewsItem, 0, len(rss.Channel.Items))
		for _, it := range rss.Channel.Items {
			published := parseFeedTime(firstNonEmpty(it.PubDate, it.Published, it.Updated))
			items = append(items, NewsItem{
				Source:    source,
				Title:     it.Title,
				Published: published,
			})
		}
		return items, nil
	}

	var atom atomFeed
	if err := xml.Unmarshal(body, &atom); err == nil && len(atom.Entries) > 0 {
		items := make([]NewsItem, 0, len(atom.Entries))
		for _, it := range atom.Entries {
			published := parseFeedTime(firstNonEmpty(it.Published, it.Updated))
			items = append(items, NewsItem{
				Source:    source,
				Title:     it.Title,
				Published: published,
			})
		}
		return items, nil
	}

	return nil, fmt.Errorf("unsupported feed format")
}

func parseFeedTime(value string) time.Time {
	if value == "" {
		return time.Time{}
	}

	layouts := []string{
		time.RFC1123Z,
		time.RFC1123,
		time.RFC822Z,
		time.RFC822,
		time.RFC3339,
		"Mon, 02 Jan 2006 15:04:05 -0700",
		"Mon, 2 Jan 2006 15:04:05 -0700",
		"2006-01-02T15:04:05Z",
	}
	for _, layout := range layouts {
		if ts, err := time.Parse(layout, value); err == nil {
			return ts
		}
	}
	return time.Time{}
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		v = strings.TrimSpace(v)
		if v != "" {
			return v
		}
	}
	return ""
}

func compactSpaces(s string) string {
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "\r", " ")
	return strings.Join(strings.Fields(strings.TrimSpace(s)), " ")
}

func dedupeNewsByTitle(items []NewsItem) []NewsItem {
	seen := make(map[string]struct{}, len(items))
	unique := make([]NewsItem, 0, len(items))
	for _, item := range items {
		key := strings.ToLower(item.Title)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		unique = append(unique, item)
	}
	return unique
}

func hashNewsID(source, title string, published time.Time) int64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(source))
	_, _ = h.Write([]byte("|"))
	_, _ = h.Write([]byte(title))
	_, _ = h.Write([]byte("|"))
	_, _ = h.Write([]byte(published.UTC().Format(time.RFC3339)))
	return int64(h.Sum64())
}
