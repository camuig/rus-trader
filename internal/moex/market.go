package moex

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

const topTickersURL = "https://iss.moex.com/iss/engines/stock/markets/shares/boards/TQBR/securities.json?iss.meta=off&iss.only=marketdata&marketdata.columns=SECID,VALTODAY,LAST&sort_column=VALTODAY&sort_order=desc"

type issResponse struct {
	Marketdata struct {
		Columns []string        `json:"columns"`
		Data    [][]interface{} `json:"data"`
	} `json:"marketdata"`
}

func (c *Client) FetchTopTickers(ctx context.Context, limit int) ([]MarketTicker, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, topTickersURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch top tickers: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("MOEX ISS returned status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	var iss issResponse
	if err := json.Unmarshal(body, &iss); err != nil {
		return nil, fmt.Errorf("parse ISS response: %w", err)
	}

	var result []MarketTicker
	for _, row := range iss.Marketdata.Data {
		if len(row) < 3 {
			continue
		}

		ticker, _ := row[0].(string)
		if ticker == "" {
			continue
		}

		lastPrice := toFloat64(row[2])
		if lastPrice == 0 {
			continue // приостановленные торги
		}

		valToday := toFloat64(row[1])

		result = append(result, MarketTicker{
			Ticker:    ticker,
			ValToday:  valToday,
			LastPrice: lastPrice,
		})

		if len(result) >= limit {
			break
		}
	}

	return result, nil
}

func toFloat64(v interface{}) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case int:
		return float64(n)
	case int64:
		return float64(n)
	default:
		return 0
	}
}
