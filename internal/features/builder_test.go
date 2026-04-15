package features

import (
	"strings"
	"testing"
	"time"

	"github.com/camuig/rus-trader/internal/broker"
	"github.com/camuig/rus-trader/internal/dividends"
	"github.com/camuig/rus-trader/internal/indicators"
)

// containsAny возвращает true если строка содержит хотя бы одну из подстрок (без учёта регистра).
func containsAny(s string, substrs ...string) bool {
	lower := strings.ToLower(s)
	for _, sub := range substrs {
		if strings.Contains(lower, strings.ToLower(sub)) {
			return true
		}
	}
	return false
}

// snap создаёт тестовый CandleSnapshot с заданными параметрами.
func snap(ticker string, price float64, ind indicators.Indicators) broker.CandleSnapshot {
	return broker.CandleSnapshot{
		Ticker:    ticker,
		LastPrice: price,
		Period1d:  broker.PeriodOHLCV{Open: price * 0.99, High: price * 1.01, Low: price * 0.98, Close: price, Volume: 1e6},
		Period3d:  broker.PeriodOHLCV{Open: price * 0.97, High: price * 1.02, Low: price * 0.96, Close: price, Volume: 3e6},
		Indicators: ind,
	}
}

func TestBuildFeatures_Uptrend(t *testing.T) {
	ind := indicators.Indicators{
		EMA9:      306,
		EMA21:     303,
		RelVolume: 1.8,
		RSI14:     58,
		ATR14:     3.0,
	}
	s := snap("SBER", 305.20, ind)
	tf := BuildTickerFeatures(s, nil)

	if !containsAny(tf.Summary, "аптренд") {
		t.Errorf("ожидался аптренд, получено: %s", tf.Summary)
	}
	if !containsAny(tf.Summary, "1.8") {
		t.Errorf("ожидался объём 1.8x, получено: %s", tf.Summary)
	}
}

func TestBuildFeatures_Downtrend(t *testing.T) {
	ind := indicators.Indicators{
		EMA9:      148,
		EMA21:     153,
		RelVolume: 1.0,
		RSI14:     45,
		ATR14:     2.0,
	}
	s := snap("VTBR", 150.0, ind)
	tf := BuildTickerFeatures(s, nil)

	if !containsAny(tf.Summary, "даунтренд") {
		t.Errorf("ожидался даунтренд, получено: %s", tf.Summary)
	}
}

func TestBuildFeatures_WithDividend(t *testing.T) {
	ind := indicators.Indicators{
		EMA9:      305,
		EMA21:     303,
		RelVolume: 1.2,
		RSI14:     55,
		ATR14:     3.0,
	}
	s := snap("SBER", 305.0, ind)
	div := &dividends.DividendInfo{
		Ticker:    "SBER",
		YieldPct:  8.2,
		ExDivDate: time.Now().Add(12 * 24 * time.Hour),
	}
	tf := BuildTickerFeatures(s, div)

	if !containsAny(tf.Summary, "дивиденд") {
		t.Errorf("ожидался дивиденд, получено: %s", tf.Summary)
	}
	if !containsAny(tf.Summary, "8.2%") {
		t.Errorf("ожидалась доходность 8.2%%, получено: %s", tf.Summary)
	}
}

func TestBuildFeatures_NearResistance(t *testing.T) {
	ind := indicators.Indicators{
		EMA9:       101,
		EMA21:      99,
		RelVolume:  1.1,
		RSI14:      55,
		ATR14:      2.0,
		Resistance: 101.0,
		Support:    98.0,
	}
	s := snap("GAZP", 100.0, ind)
	tf := BuildTickerFeatures(s, nil)

	if !containsAny(tf.Summary, "сопротивл") {
		t.Errorf("ожидалось упоминание сопротивления, получено: %s", tf.Summary)
	}
}

func TestBuildFeatures_Oversold(t *testing.T) {
	ind := indicators.Indicators{
		EMA9:      98,
		EMA21:     100,
		RelVolume: 0.8,
		RSI14:     25,
		ATR14:     1.5,
	}
	s := snap("LKOH", 200.0, ind)
	tf := BuildTickerFeatures(s, nil)

	if !containsAny(tf.Summary, "перепродан") {
		t.Errorf("ожидался RSI перепродан, получено: %s", tf.Summary)
	}
}

func TestBuildFeatures_EmptyIndicators(t *testing.T) {
	s := snap("MOEX", 150.0, indicators.Indicators{})
	tf := BuildTickerFeatures(s, nil)

	if tf.Summary == "" {
		t.Error("ожидалась непустая строка summary")
	}
	if !containsAny(tf.Summary, "MOEX") {
		t.Errorf("ожидался тикер MOEX в summary, получено: %s", tf.Summary)
	}
	if !containsAny(tf.Summary, "150") {
		t.Errorf("ожидалась цена 150 в summary, получено: %s", tf.Summary)
	}
}
