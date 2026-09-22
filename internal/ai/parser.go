package ai

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

var (
	thinkTagRegex  = regexp.MustCompile(`(?s)<think>.*?</think>`)
	codeFenceRegex = regexp.MustCompile("(?si)```\\w*\\s*\n(.*?)\n\\s*```")
)

// StripThinkTags removes <think> reasoning tags that some providers inline into the response.
func StripThinkTags(text string) string {
	return strings.TrimSpace(thinkTagRegex.ReplaceAllString(text, ""))
}

// stripCodeFences extracts content from markdown code fences if present.
func stripCodeFences(text string) string {
	if match := codeFenceRegex.FindStringSubmatch(text); len(match) > 1 {
		return strings.TrimSpace(match[1])
	}
	return strings.TrimSpace(text)
}

// ParseDecisions parses AI response into a slice of decisions.
// Handles: JSON array, single JSON object, markdown code fences.
func ParseDecisions(text string) ([]AIDecision, error) {
	cleaned := StripThinkTags(text)
	cleaned = stripCodeFences(cleaned)

	if cleaned == "" || cleaned == "[]" {
		return nil, nil
	}

	// Try parsing as array first
	var decisions []AIDecision
	if err := json.Unmarshal([]byte(cleaned), &decisions); err == nil {
		return normalizeDecisions(decisions), nil
	}

	// Try parsing as single object
	var single AIDecision
	if err := json.Unmarshal([]byte(cleaned), &single); err == nil {
		return normalizeDecisions([]AIDecision{single}), nil
	}

	// Try to extract JSON from the text
	jsonStart := strings.Index(cleaned, "[")
	jsonEnd := strings.LastIndex(cleaned, "]")
	if jsonStart >= 0 && jsonEnd > jsonStart {
		substr := cleaned[jsonStart : jsonEnd+1]
		if err := json.Unmarshal([]byte(substr), &decisions); err == nil {
			return normalizeDecisions(decisions), nil
		}
	}

	// Try extracting a single JSON object
	jsonStart = strings.Index(cleaned, "{")
	jsonEnd = strings.LastIndex(cleaned, "}")
	if jsonStart >= 0 && jsonEnd > jsonStart {
		substr := cleaned[jsonStart : jsonEnd+1]
		if err := json.Unmarshal([]byte(substr), &single); err == nil {
			return normalizeDecisions([]AIDecision{single}), nil
		}
	}

	return nil, fmt.Errorf("failed to parse AI response as JSON: %.200s", cleaned)
}

// normalizeDecisions приводит confidence BUY-решений к шкале 0-100: разные модели
// возвращают долю (0.85) вместо процентов (85), и без нормализации такая сделка
// тихо отсеивалась бы порогом trading.min_confidence.
func normalizeDecisions(decisions []AIDecision) []AIDecision {
	for i := range decisions {
		d := &decisions[i]
		if d.Action == "BUY" && d.Confidence > 0 && d.Confidence <= 1 {
			d.Confidence *= 100
		}
	}
	return decisions
}
