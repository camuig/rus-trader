package sentiment

import (
	"strings"
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
		{0.0, "neutral"},
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
	if !strings.Contains(prompt, "SBER") || !strings.Contains(prompt, "GAZP") {
		t.Error("prompt should mention both tickers")
	}
	if !strings.Contains(prompt, "Рекордные дивиденды") {
		t.Error("prompt should include news text")
	}
}

func TestBuildSentimentPrompt_Empty(t *testing.T) {
	prompt := BuildSentimentPrompt(map[string][]string{})
	if !strings.Contains(prompt, "sentiment") {
		t.Error("expected header in empty prompt")
	}
}
