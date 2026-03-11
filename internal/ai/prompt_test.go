package ai

import (
	"strings"
	"testing"
)

func TestBuildUserPrompt_IncludesBriefsAndWorldNews(t *testing.T) {
	req := &AnalysisRequest{
		Tickers: []TickerAnalysis{
			{
				Ticker:    "SBER",
				Brief:     "Сбербанк; share; лот 10; RUB; Россия",
				LastPrice: 280.12,
				Period3h:  PeriodData{Open: 279, High: 281, Low: 278, Close: 280, Volume: 1200000, ChangePct: 0.4},
				Period1d:  PeriodData{Open: 275, High: 282, Low: 274, Close: 280, Volume: 5200000, ChangePct: 1.8},
				Period3d:  PeriodData{Open: 270, High: 283, Low: 269, Close: 280, Volume: 12400000, ChangePct: 3.7},
				Period1w:  PeriodData{Open: 265, High: 284, Low: 263, Close: 280, Volume: 25100000, ChangePct: 5.6},
				News:      []string{"Сбербанк опубликовал сильный отчет"},
			},
		},
		GlobalNews: []string{
			"Reuters World: Oil rises on supply concerns",
			"Reuters Business: Fed officials signal cautious approach",
		},
		AvailableRub: 100000,
		TotalRub:     150000,
	}

	prompt := BuildUserPrompt(req, nil, PromptLimits{
		MaxChars:            12000,
		MaxTickerBriefChars: 900,
		MaxTickerNewsItems:  8,
		MaxWorldNewsItems:   5,
		MaxNewsTitleChars:   120,
	})

	if !strings.Contains(prompt, "## Кратко по тикерам") {
		t.Fatalf("expected ticker briefs section in prompt")
	}
	if !strings.Contains(prompt, "SBER: Сбербанк; share; лот 10; RUB; Россия") {
		t.Fatalf("expected brief line in prompt")
	}
	if !strings.Contains(prompt, "## Общемировой фон (24ч)") {
		t.Fatalf("expected world news section in prompt")
	}
}

func TestBuildUserPrompt_RespectsMaxChars(t *testing.T) {
	veryLongNews := strings.Repeat("Очень длинный заголовок новости ", 30)
	req := &AnalysisRequest{
		Tickers: []TickerAnalysis{
			{
				Ticker:    "SBER",
				Brief:     strings.Repeat("brief ", 80),
				LastPrice: 280.12,
				Period3h:  PeriodData{Open: 279, High: 281, Low: 278, Close: 280, Volume: 1200000, ChangePct: 0.4},
				Period1d:  PeriodData{Open: 275, High: 282, Low: 274, Close: 280, Volume: 5200000, ChangePct: 1.8},
				Period3d:  PeriodData{Open: 270, High: 283, Low: 269, Close: 280, Volume: 12400000, ChangePct: 3.7},
				Period1w:  PeriodData{Open: 265, High: 284, Low: 263, Close: 280, Volume: 25100000, ChangePct: 5.6},
				News:      []string{veryLongNews, veryLongNews, veryLongNews},
			},
		},
		GlobalNews:   []string{veryLongNews, veryLongNews, veryLongNews},
		AvailableRub: 100000,
		TotalRub:     150000,
	}

	limit := 420
	prompt := BuildUserPrompt(req, nil, PromptLimits{
		MaxChars:            limit,
		MaxTickerBriefChars: 120,
		MaxTickerNewsItems:  2,
		MaxWorldNewsItems:   1,
		MaxNewsTitleChars:   80,
	})

	if runeLen(prompt) > limit {
		t.Fatalf("prompt length exceeded limit: got %d, want <= %d", runeLen(prompt), limit)
	}
	if !strings.Contains(prompt, "JSON") {
		t.Fatalf("expected prompt tail instruction")
	}
}

func TestBuildUserPrompt_RespectsNewsItemLimits(t *testing.T) {
	req := &AnalysisRequest{
		Tickers: []TickerAnalysis{
			{
				Ticker:    "SBER",
				LastPrice: 280.12,
				Period3h:  PeriodData{Open: 279, High: 281, Low: 278, Close: 280, Volume: 1200000, ChangePct: 0.4},
				Period1d:  PeriodData{Open: 275, High: 282, Low: 274, Close: 280, Volume: 5200000, ChangePct: 1.8},
				Period3d:  PeriodData{Open: 270, High: 283, Low: 269, Close: 280, Volume: 12400000, ChangePct: 3.7},
				Period1w:  PeriodData{Open: 265, High: 284, Low: 263, Close: 280, Volume: 25100000, ChangePct: 5.6},
				News:      []string{"N1", "N2", "N3"},
			},
		},
		GlobalNews:   []string{"W1", "W2", "W3"},
		AvailableRub: 100000,
		TotalRub:     150000,
	}

	prompt := BuildUserPrompt(req, nil, PromptLimits{
		MaxChars:            12000,
		MaxTickerBriefChars: 900,
		MaxTickerNewsItems:  2,
		MaxWorldNewsItems:   1,
		MaxNewsTitleChars:   120,
	})

	if strings.Count(prompt, "SBER: N") > 2 {
		t.Fatalf("ticker news section exceeds configured item limit")
	}
	if strings.Count(prompt, "- W") > 1 {
		t.Fatalf("world news section exceeds configured item limit")
	}
}
