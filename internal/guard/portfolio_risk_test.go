package guard

import (
	"strings"
	"testing"

	"github.com/camuig/rus-trader/internal/ai"
	"github.com/camuig/rus-trader/internal/config"
	"github.com/camuig/rus-trader/internal/indicators"
	"github.com/camuig/rus-trader/internal/storage"
)

func TestOpenPortfolioRisk(t *testing.T) {
	trades := []storage.Trade{
		{Ticker: "SBER", Price: 100, StopLossPrice: 97, Quantity: 10}, // риск 30
		{Ticker: "BAZA", Price: 95, StopLossPrice: 93, Quantity: 5},   // риск 2×5×lot(10) = 100
		{Ticker: "NOSL", Price: 50, StopLossPrice: 0, Quantity: 100},  // без SL — не считается
		{Ticker: "BADSL", Price: 30, StopLossPrice: 36, Quantity: 10}, // SL выше входа — не считается
	}
	lotSizes := map[string]int64{"BAZA": 10}
	lotFn := func(ticker string) int64 {
		if ls, ok := lotSizes[ticker]; ok {
			return ls
		}
		return 1
	}

	got := openPortfolioRisk(trades, lotFn)
	if got != 130 {
		t.Fatalf("openPortfolioRisk = %v, want 130", got)
	}

	// Без lotSizeFn лот считается равным 1.
	if got := openPortfolioRisk(trades, nil); got != 40 {
		t.Fatalf("openPortfolioRisk(nil fn) = %v, want 40", got)
	}
}

func TestEstimateNewTradeRisk(t *testing.T) {
	g, _ := newTestGuard(t, config.TradingConfig{
		MaxPositionRub:     10000,
		DefaultStopLossPct: 3.0,
		MinStopLossPct:     2.0,
	})

	// Без индикаторов: дефолтный SL 3%, confidence 70 → позиция 50%.
	risk := g.estimateNewTradeRisk(ai.AIDecision{Ticker: "SBER", Confidence: 70})
	if risk != 150 {
		t.Fatalf("risk = %v, want 150 (5000 × 3%%)", risk)
	}

	// SL из решения относительно EMA9: (100−95)/100 = 5%, confidence 90 → полная позиция.
	g.SetIndicators(map[string]indicators.Indicators{"SBER": {EMA9: 100}})
	risk = g.estimateNewTradeRisk(ai.AIDecision{Ticker: "SBER", Confidence: 90, StopLoss: 95})
	if risk != 500 {
		t.Fatalf("risk = %v, want 500 (10000 × 5%%)", risk)
	}

	// SL шире кэпа 6% обрезается: (100−80)/100 = 20% → 6%.
	risk = g.estimateNewTradeRisk(ai.AIDecision{Ticker: "SBER", Confidence: 90, StopLoss: 80})
	if risk != 600 {
		t.Fatalf("risk = %v, want 600 (кэп 6%%)", risk)
	}
}

func TestCheckBuy_PortfolioRiskLimit(t *testing.T) {
	trading := config.TradingConfig{
		MaxPositionRub:      10000,
		DefaultStopLossPct:  3.0,
		MinStopLossPct:      2.0,
		MaxOpenPositions:    10,
		MaxDailyTrades:      100,
		MaxPortfolioRiskRub: 100,
	}
	g, repo := newTestGuard(t, trading)

	// Открытая позиция с риском (100−97)×10 = 30 ₽.
	saveTrade(t, repo, &storage.Trade{
		Ticker: "GAZP", Action: "BUY", Price: 100, StopLossPrice: 97,
		Quantity: 10, Status: "open",
	})

	// Новый BUY с риском 150 ₽: 30+150 > 100 → блок.
	allowed, blocked := g.Filter([]ai.AIDecision{{Action: "BUY", Ticker: "SBER", Confidence: 70}})
	if len(allowed) != 0 || len(blocked) != 1 {
		t.Fatalf("expected block, got allowed=%d blocked=%d", len(allowed), len(blocked))
	}
	if !strings.Contains(blocked[0].Reason, "совокупного риска") {
		t.Fatalf("reason = %q, want portfolio risk mention", blocked[0].Reason)
	}
}

func TestCheckBuy_PortfolioRiskAccumulatesWithinCycle(t *testing.T) {
	trading := config.TradingConfig{
		MaxPositionRub:      10000,
		DefaultStopLossPct:  3.0,
		MinStopLossPct:      2.0,
		MaxOpenPositions:    10,
		MaxDailyTrades:      100,
		MaxPortfolioRiskRub: 200,
	}
	g, _ := newTestGuard(t, trading)

	// Каждый BUY несёт риск 150 ₽: первый проходит (150 ≤ 200),
	// второй должен быть заблокирован (150+150 > 200).
	allowed, blocked := g.Filter([]ai.AIDecision{
		{Action: "BUY", Ticker: "SBER", Confidence: 70},
		{Action: "BUY", Ticker: "LKOH", Confidence: 70},
	})
	if len(allowed) != 1 || len(blocked) != 1 {
		t.Fatalf("expected 1 allowed + 1 blocked, got allowed=%d blocked=%d", len(allowed), len(blocked))
	}
	if !strings.Contains(blocked[0].Reason, "совокупного риска") {
		t.Fatalf("reason = %q, want portfolio risk mention", blocked[0].Reason)
	}
}
