package broker

import (
	"fmt"
	"strings"
	"sync"
	"time"
)

var instrumentCache sync.Map // instrumentUID -> ticker
var instrumentBriefCache sync.Map

const briefCacheTTL = 24 * time.Hour

type briefCacheEntry struct {
	value     string
	expiresAt time.Time
}

func (bc *BrokerClient) resolveInstrumentUID(uid string) (string, error) {
	if cached, ok := instrumentCache.Load(uid); ok {
		return cached.(string), nil
	}

	instruments := bc.Client.NewInstrumentsServiceClient()
	resp, err := instruments.InstrumentByUid(uid)
	if err != nil {
		return "", fmt.Errorf("instrument by uid %s: %w", uid, err)
	}

	ticker := resp.GetInstrument().GetTicker()
	instrumentCache.Store(uid, ticker)
	return ticker, nil
}

// ResolveTickerToUID resolves a ticker to its instrument UID using the instruments service.
func (bc *BrokerClient) ResolveTickerToUID(ticker string) (string, error) {
	instruments := bc.Client.NewInstrumentsServiceClient()
	resp, err := instruments.FindInstrument(ticker)
	if err != nil {
		return "", fmt.Errorf("find instrument %s: %w", ticker, err)
	}

	for _, inst := range resp.GetInstruments() {
		if inst.GetTicker() == ticker {
			uid := inst.GetUid()
			instrumentCache.Store(uid, ticker)
			return uid, nil
		}
	}

	if len(resp.GetInstruments()) > 0 {
		inst := resp.GetInstruments()[0]
		uid := inst.GetUid()
		instrumentCache.Store(uid, inst.GetTicker())
		return uid, nil
	}

	return "", fmt.Errorf("instrument not found: %s", ticker)
}

func (bc *BrokerClient) GetTickerBrief(ticker string) (string, error) {
	if cached, ok := instrumentBriefCache.Load(ticker); ok {
		entry := cached.(briefCacheEntry)
		if time.Now().Before(entry.expiresAt) {
			return entry.value, nil
		}
		instrumentBriefCache.Delete(ticker)
	}

	uid, err := bc.ResolveTickerToUID(ticker)
	if err != nil {
		return "", err
	}

	instruments := bc.Client.NewInstrumentsServiceClient()
	resp, err := instruments.InstrumentByUid(uid)
	if err != nil {
		return "", fmt.Errorf("instrument by uid %s: %w", uid, err)
	}

	inst := resp.GetInstrument()
	brief := formatTickerBrief(
		inst.GetName(),
		inst.GetInstrumentType(),
		inst.GetLot(),
		inst.GetCurrency(),
		inst.GetCountryOfRiskName(),
	)

	instrumentBriefCache.Store(ticker, briefCacheEntry{
		value:     brief,
		expiresAt: time.Now().Add(briefCacheTTL),
	})

	return brief, nil
}

func formatTickerBrief(name, instrumentType string, lot int32, currency, country string) string {
	parts := make([]string, 0, 5)
	if name != "" {
		parts = append(parts, name)
	}
	if instrumentType != "" {
		parts = append(parts, instrumentType)
	}
	if lot > 0 {
		parts = append(parts, fmt.Sprintf("лот %d", lot))
	}
	if currency != "" {
		parts = append(parts, strings.ToUpper(currency))
	}
	if country != "" {
		parts = append(parts, country)
	}
	return strings.Join(parts, "; ")
}
