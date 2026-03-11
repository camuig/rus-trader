package moex

import (
	"testing"
	"time"
)

func TestParseNewsFeed_RSS(t *testing.T) {
	xmlData := []byte(`<?xml version="1.0" encoding="UTF-8"?>
<rss version="2.0">
  <channel>
    <item>
      <title>World markets rise</title>
      <pubDate>Mon, 02 Jan 2006 15:04:05 -0700</pubDate>
    </item>
  </channel>
</rss>`)

	items, err := parseNewsFeed(xmlData, "TestFeed")
	if err != nil {
		t.Fatalf("parseNewsFeed error: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}
	if items[0].Source != "TestFeed" {
		t.Fatalf("unexpected source %q", items[0].Source)
	}
	if items[0].Title != "World markets rise" {
		t.Fatalf("unexpected title %q", items[0].Title)
	}
}

func TestDedupeNewsByTitle(t *testing.T) {
	now := time.Now()
	items := []NewsItem{
		{Title: "Same title", Published: now},
		{Title: "same title", Published: now.Add(-time.Minute)},
		{Title: "Different title", Published: now},
	}

	unique := dedupeNewsByTitle(items)
	if len(unique) != 2 {
		t.Fatalf("expected 2 unique items, got %d", len(unique))
	}
}
