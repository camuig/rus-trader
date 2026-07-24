package broker

import (
	"fmt"
	"strings"
	"sync"
	"time"
)

var instrumentCache sync.Map // instrumentUID -> ticker
var instrumentBriefCache sync.Map
var lotSizeCache sync.Map       // instrumentUID -> int32 lot size
var apiTradeAvailCache sync.Map // instrumentUID -> bool (доступна ли торговля через API)

const briefCacheTTL = 24 * time.Hour

// apiTradeAvailTTL — как часто перепроверять флаг api_trade_available_flag.
// T-Invest переключает его динамически (санкции/инфраструктурные ограничения),
// поэтому кэшируем ненадолго, чтобы бот автоматически возобновил торговлю,
// как только инструмент снова станет доступен через API.
const apiTradeAvailTTL = 30 * time.Minute

type briefCacheEntry struct {
	value     string
	expiresAt time.Time
}

type apiTradeAvailEntry struct {
	value     bool
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

	inst := resp.GetInstrument()
	ticker := inst.GetTicker()
	instrumentCache.Store(uid, ticker)
	if lot := inst.GetLot(); lot > 0 {
		lotSizeCache.Store(uid, lot)
	}
	return ticker, nil
}

// GetLotSize возвращает размер лота инструмента (число акций в 1 лоте).
// Нужно для конвертации Quantity из портфеля (акции) в лоты (единица измерения в Trade/Order).
func (bc *BrokerClient) GetLotSize(uid string) (int32, error) {
	if cached, ok := lotSizeCache.Load(uid); ok {
		return cached.(int32), nil
	}

	instruments := bc.Client.NewInstrumentsServiceClient()
	resp, err := instruments.InstrumentByUid(uid)
	if err != nil {
		return 0, fmt.Errorf("instrument by uid %s: %w", uid, err)
	}

	lot := resp.GetInstrument().GetLot()
	if lot > 0 {
		lotSizeCache.Store(uid, lot)
	}
	return lot, nil
}

// IsAPITradeAvailable сообщает, доступен ли инструмент для торговли через API
// (флаг api_trade_available_flag). Когда он false, T-Invest отклоняет PostOrder
// с кодом 30052 ("Instrument forbidden for trading by API") — ставить ордер
// бессмысленно. Результат кэшируется на время жизни процесса.
//
// При ошибке запроса справки возвращаем true (fail-open): не блокируем торговлю
// из-за временного сбоя получения данных и не кэшируем такой ответ.
func (bc *BrokerClient) IsAPITradeAvailable(uid string) bool {
	if cached, ok := apiTradeAvailCache.Load(uid); ok {
		entry := cached.(apiTradeAvailEntry)
		if time.Now().Before(entry.expiresAt) {
			return entry.value
		}
		apiTradeAvailCache.Delete(uid)
	}

	instruments := bc.Client.NewInstrumentsServiceClient()
	resp, err := instruments.InstrumentByUid(uid)
	if err != nil {
		return true
	}

	available := resp.GetInstrument().GetApiTradeAvailableFlag()
	apiTradeAvailCache.Store(uid, apiTradeAvailEntry{
		value:     available,
		expiresAt: time.Now().Add(apiTradeAvailTTL),
	})
	return available
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
