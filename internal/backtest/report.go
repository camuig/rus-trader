package backtest

import (
	"encoding/json"
	"fmt"
	"strings"
)

func FormatText(r *BacktestResult) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("=== Backtest: %s ===\n\n", r.Period))
	if len(r.ParamsChanged) > 0 {
		sb.WriteString("Parameters changed:\n")
		for k, v := range r.ParamsChanged {
			sb.WriteString(fmt.Sprintf("  %s: %s\n", k, v))
		}
		sb.WriteString("\n")
	} else {
		sb.WriteString("Parameters: unchanged (baseline)\n\n")
	}
	sb.WriteString("Real results:\n")
	sb.WriteString(formatStats(r.Real))
	sb.WriteString("\nTest results:\n")
	sb.WriteString(formatStats(r.Test))
	sb.WriteString(fmt.Sprintf("\nImpact:\n  Filtered out: %d trades (their P&L: %+.0f ₽)\n  Newly allowed: %d trades (their P&L: %+.0f ₽)\n\n",
		r.FilteredOut, r.FilteredOutPnL, r.NewAllowed, r.NewAllowedPnL))
	diff := r.Test.TotalPnL - r.Real.TotalPnL
	verdict := "SAME"
	if diff > 0 {
		verdict = fmt.Sprintf("BETTER by %+.0f ₽", diff)
	} else if diff < 0 {
		verdict = fmt.Sprintf("WORSE by %.0f ₽", diff)
	}
	sb.WriteString(fmt.Sprintf("Verdict: Test params %s\n", verdict))
	return sb.String()
}

func formatStats(s ResultStats) string {
	return fmt.Sprintf("  Trades: %d | Wins: %d | Losses: %d | WR: %.0f%% | P&L: %+.0f ₽\n  Max DD: %.0f ₽ | Avg win: %+.0f ₽ | Avg loss: %.0f ₽\n",
		s.Trades, s.Wins, s.Losses, s.WinRate, s.TotalPnL,
		s.MaxDrawdown, s.AvgWinPnL, s.AvgLossPnL)
}

func FormatJSON(r *BacktestResult) ([]byte, error) {
	return json.MarshalIndent(r, "", "  ")
}
