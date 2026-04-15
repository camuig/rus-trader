package journal

import (
	"strings"
	"testing"
	"time"
)

func TestClassifyExitReason(t *testing.T) {
	cases := []struct {
		input    string
		expected string
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

	for _, tc := range cases {
		got := ClassifyExitReason(tc.input)
		if got != tc.expected {
			t.Errorf("ClassifyExitReason(%q) = %q, want %q", tc.input, got, tc.expected)
		}
	}
}

func TestComputeWhatHappened(t *testing.T) {
	result := ComputeWhatHappened(100, 97, 3.5)

	if !strings.Contains(result, "упала") {
		t.Errorf("expected 'упала' in result, got: %q", result)
	}
	if !strings.Contains(result, "-3") {
		t.Errorf("expected '-3' in result, got: %q", result)
	}
}

func TestComputeWhatHappened_Profit(t *testing.T) {
	result := ComputeWhatHappened(100, 105, 8.0)

	if !strings.Contains(result, "выросла") {
		t.Errorf("expected 'выросла' in result, got: %q", result)
	}
	if !strings.Contains(result, "+5") {
		t.Errorf("expected '+5' in result, got: %q", result)
	}
}

func TestComputeWhatHappened_ZeroEntry(t *testing.T) {
	result := ComputeWhatHappened(0, 105, 8.0)
	if result != "" {
		t.Errorf("expected empty string for zero entry price, got: %q", result)
	}
}

func TestPreviousTradingDay(t *testing.T) {
	loc := time.UTC

	// Monday 10:00 → previous Friday
	monday := time.Date(2024, 4, 8, 10, 0, 0, 0, loc) // Monday
	prev := PreviousTradingDay(monday)
	if prev.Weekday() != time.Friday {
		t.Errorf("Monday → expected Friday, got %s", prev.Weekday())
	}
	if prev.Day() != 5 {
		t.Errorf("Monday April 8 → expected April 5 (Friday), got %s", prev.Format("2006-01-02"))
	}

	// Saturday → previous Friday
	saturday := time.Date(2024, 4, 6, 12, 0, 0, 0, loc) // Saturday
	prev = PreviousTradingDay(saturday)
	if prev.Weekday() != time.Friday {
		t.Errorf("Saturday → expected Friday, got %s", prev.Weekday())
	}
	if prev.Day() != 5 {
		t.Errorf("Saturday April 6 → expected April 5 (Friday), got %s", prev.Format("2006-01-02"))
	}

	// Wednesday → previous Tuesday
	wednesday := time.Date(2024, 4, 10, 9, 0, 0, 0, loc) // Wednesday
	prev = PreviousTradingDay(wednesday)
	if prev.Weekday() != time.Tuesday {
		t.Errorf("Wednesday → expected Tuesday, got %s", prev.Weekday())
	}
	if prev.Day() != 9 {
		t.Errorf("Wednesday April 10 → expected April 9 (Tuesday), got %s", prev.Format("2006-01-02"))
	}

	// Sunday → previous Friday
	sunday := time.Date(2024, 4, 7, 15, 0, 0, 0, loc) // Sunday
	prev = PreviousTradingDay(sunday)
	if prev.Weekday() != time.Friday {
		t.Errorf("Sunday → expected Friday, got %s", prev.Weekday())
	}
	if prev.Day() != 5 {
		t.Errorf("Sunday April 7 → expected April 5 (Friday), got %s", prev.Format("2006-01-02"))
	}
}
