package ai

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

var thinkTagRegex = regexp.MustCompile(`(?s)<think>.*?</think>`)

// StripThinkTags removes DeepSeek R1 reasoning tags from the response.
func StripThinkTags(text string) string {
	return strings.TrimSpace(thinkTagRegex.ReplaceAllString(text, ""))
}

// ParseDecisions parses AI response into a slice of decisions.
// Handles: JSON array, single JSON object, markdown code fences.
func ParseDecisions(text string) ([]AIDecision, error) {
	cleaned := StripThinkTags(text)

	// Remove markdown code fences
	cleaned = strings.TrimPrefix(cleaned, "```json")
	cleaned = strings.TrimPrefix(cleaned, "```")
	cleaned = strings.TrimSuffix(cleaned, "```")
	cleaned = strings.TrimSpace(cleaned)

	if cleaned == "" || cleaned == "[]" {
		return nil, nil
	}

	// Try parsing as array first
	var decisions []AIDecision
	if err := json.Unmarshal([]byte(cleaned), &decisions); err == nil {
		return decisions, nil
	}

	// Try parsing as single object
	var single AIDecision
	if err := json.Unmarshal([]byte(cleaned), &single); err == nil {
		return []AIDecision{single}, nil
	}

	// Try to extract JSON from the text
	jsonStart := strings.Index(cleaned, "[")
	jsonEnd := strings.LastIndex(cleaned, "]")
	if jsonStart >= 0 && jsonEnd > jsonStart {
		substr := cleaned[jsonStart : jsonEnd+1]
		if err := json.Unmarshal([]byte(substr), &decisions); err == nil {
			return decisions, nil
		}
	}

	// Try extracting a single JSON object
	jsonStart = strings.Index(cleaned, "{")
	jsonEnd = strings.LastIndex(cleaned, "}")
	if jsonStart >= 0 && jsonEnd > jsonStart {
		substr := cleaned[jsonStart : jsonEnd+1]
		if err := json.Unmarshal([]byte(substr), &single); err == nil {
			return []AIDecision{single}, nil
		}
	}

	return nil, fmt.Errorf("failed to parse AI response as JSON: %.200s", cleaned)
}
