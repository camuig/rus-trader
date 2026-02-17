package moex

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const newsBaseURL = "https://iss.moex.com/iss/sitenews.json?lang=ru"

// tickerToNames maps tickers to Russian company names for news matching.
var tickerToNames = map[string][]string{
	"SBER":  {"Сбербанк", "Сбер"},
	"GAZP":  {"Газпром"},
	"LKOH":  {"Лукойл", "ЛУКОЙЛ"},
	"GMKN":  {"Норникель", "Норильский никель"},
	"NVTK":  {"Новатэк", "НОВАТЭК"},
	"ROSN":  {"Роснефть"},
	"YNDX":  {"Яндекс"},
	"TCSG":  {"Тинькофф", "Т-Банк", "TCS"},
	"MTSS":  {"МТС"},
	"MGNT":  {"Магнит"},
	"PLZL":  {"Полюс"},
	"CHMF":  {"Северсталь"},
	"ALRS":  {"Алроса", "АЛРОСА"},
	"SNGS":  {"Сургутнефтегаз"},
	"VTBR":  {"ВТБ"},
	"MOEX":  {"Мосбиржа", "Московская биржа"},
	"TATN":  {"Татнефть"},
	"NLMK":  {"НЛМК"},
	"PHOR":  {"ФосАгро"},
	"IRAO":  {"Интер РАО"},
}

type issNewsResponse struct {
	SiteNews struct {
		Columns []string        `json:"columns"`
		Data    [][]interface{} `json:"data"`
	} `json:"sitenews"`
}

func (c *Client) FetchRecentNews(ctx context.Context) ([]NewsItem, error) {
	var allNews []NewsItem
	cutoff := time.Now().Add(-24 * time.Hour)

	for page := 0; page < 4; page++ {
		url := fmt.Sprintf("%s&start=%d", newsBaseURL, page*50)

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return nil, fmt.Errorf("create news request: %w", err)
		}

		resp, err := c.httpClient.Do(req)
		if err != nil {
			return nil, fmt.Errorf("fetch news page %d: %w", page, err)
		}

		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("MOEX news returned status %d", resp.StatusCode)
		}

		if err != nil {
			return nil, fmt.Errorf("read news response: %w", err)
		}

		var iss issNewsResponse
		if err := json.Unmarshal(body, &iss); err != nil {
			return nil, fmt.Errorf("parse news response: %w", err)
		}

		// Find column indices
		idIdx, titleIdx, pubIdx := -1, -1, -1
		for i, col := range iss.SiteNews.Columns {
			switch col {
			case "id":
				idIdx = i
			case "title":
				titleIdx = i
			case "published_at":
				pubIdx = i
			}
		}
		if idIdx < 0 || titleIdx < 0 || pubIdx < 0 {
			return nil, fmt.Errorf("unexpected news columns: %v", iss.SiteNews.Columns)
		}

		stoppedEarly := false
		for _, row := range iss.SiteNews.Data {
			if len(row) <= pubIdx || len(row) <= titleIdx || len(row) <= idIdx {
				continue
			}

			pubStr, _ := row[pubIdx].(string)
			published, err := time.Parse("2006-01-02 15:04:05", pubStr)
			if err != nil {
				continue
			}

			if published.Before(cutoff) {
				stoppedEarly = true
				break
			}

			id := int64(toFloat64(row[idIdx]))
			title, _ := row[titleIdx].(string)

			allNews = append(allNews, NewsItem{
				ID:        id,
				Title:     title,
				Published: published,
			})
		}

		if stoppedEarly || len(iss.SiteNews.Data) < 50 {
			break
		}
	}

	return allNews, nil
}

// FilterNewsForTickers returns news items grouped by ticker, matching by ticker symbol or Russian company name in the title.
func FilterNewsForTickers(news []NewsItem, tickers []string) map[string][]NewsItem {
	result := make(map[string][]NewsItem)

	for _, ticker := range tickers {
		searchTerms := []string{strings.ToUpper(ticker)}
		if names, ok := tickerToNames[ticker]; ok {
			searchTerms = append(searchTerms, names...)
		}

		for _, item := range news {
			titleUpper := strings.ToUpper(item.Title)
			for _, term := range searchTerms {
				if strings.Contains(titleUpper, strings.ToUpper(term)) {
					result[ticker] = append(result[ticker], item)
					break
				}
			}
		}
	}

	return result
}
