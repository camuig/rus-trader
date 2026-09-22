package ai

import "testing"

func TestParseDecisionsNormalizesBuyConfidence(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want float64
	}{
		{"fraction is scaled to percent", `[{"action":"BUY","ticker":"SBER","confidence":0.85}]`, 85},
		{"percent is kept", `[{"action":"BUY","ticker":"SBER","confidence":85}]`, 85},
		{"exactly 1 is treated as fraction", `[{"action":"BUY","ticker":"SBER","confidence":1}]`, 100},
		{"zero is kept", `[{"action":"BUY","ticker":"SBER","confidence":0}]`, 0},
		{"position manager fraction is untouched", `[{"action":"SELL","ticker":"SBER","confidence":0.8}]`, 0.8},
		{"single object with code fence", "```json\n{\"action\":\"BUY\",\"ticker\":\"SBER\",\"confidence\":0.7}\n```", 70},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			decs, err := ParseDecisions(tt.raw)
			if err != nil {
				t.Fatalf("ParseDecisions() error = %v", err)
			}
			if len(decs) != 1 {
				t.Fatalf("got %d decisions, want 1", len(decs))
			}
			if decs[0].Confidence != tt.want {
				t.Errorf("Confidence = %v, want %v", decs[0].Confidence, tt.want)
			}
		})
	}
}

func TestParseDecisionsEmpty(t *testing.T) {
	for _, raw := range []string{"", "[]", "<think>размышления</think>[]", "```json\n[]\n```"} {
		decs, err := ParseDecisions(raw)
		if err != nil {
			t.Errorf("ParseDecisions(%q) error = %v", raw, err)
		}
		if len(decs) != 0 {
			t.Errorf("ParseDecisions(%q) = %v, want empty", raw, decs)
		}
	}
}
