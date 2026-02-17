package broker

import (
	"fmt"
	"sync"
)

var instrumentCache sync.Map // instrumentUID -> ticker

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
