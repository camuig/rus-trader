package dividends

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/html"

	"github.com/camuig/rus-trader/internal/logger"
)

const smartlabURL = "https://smart-lab.ru/dividends/index/order_by_yield/desc/"

// DividendInfo содержит данные о дивиденде для конкретного тикера.
type DividendInfo struct {
	Ticker      string
	CompanyName string
	DividendRub float64
	YieldPct    float64
	ExDivDate   time.Time // колонка "Купить До" — последний день покупки
	RecordDate  time.Time // колонка "Дата закрытия реестра"
	Status      string    // "approved" или "forecast"
}

// Fetcher загружает и кэширует данные о дивидендах со SmartLab.
type Fetcher struct {
	cache     sync.Map
	lastFetch time.Time
	ttl       time.Duration
	logger    *logger.Logger
	mu        sync.Mutex
}

// NewFetcher создаёт новый Fetcher с заданным TTL кэша.
func NewFetcher(ttl time.Duration, log *logger.Logger) *Fetcher {
	return &Fetcher{
		ttl:    ttl,
		logger: log,
	}
}

// Fetch загружает данные о дивидендах, используя кэш при наличии актуальных данных.
// При ошибке HTTP возвращает устаревший кэш (graceful degradation).
func (f *Fetcher) Fetch(ctx context.Context) (map[string]DividendInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if !f.lastFetch.IsZero() && time.Since(f.lastFetch) < f.ttl {
		return f.fromCache(), nil
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, smartlabURL, nil)
	if err != nil {
		if cached := f.fromCache(); len(cached) > 0 {
			if f.logger != nil {
				f.logger.Infof("dividends: HTTP request create failed, using stale cache: %v", err)
			}
			return cached, nil
		}
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; rus-trader/1.0)")

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		if cached := f.fromCache(); len(cached) > 0 {
			if f.logger != nil {
				f.logger.Infof("dividends: HTTP fetch failed, using stale cache: %v", err)
			}
			return cached, nil
		}
		return nil, fmt.Errorf("fetch smartlab: %w", err)
	}
	defer resp.Body.Close()

	divs, err := parseHTML(resp.Body)
	if err != nil {
		if cached := f.fromCache(); len(cached) > 0 {
			if f.logger != nil {
				f.logger.Infof("dividends: HTML parse failed, using stale cache: %v", err)
			}
			return cached, nil
		}
		return nil, fmt.Errorf("parse HTML: %w", err)
	}

	// Обновляем кэш
	f.cache.Range(func(k, _ any) bool {
		f.cache.Delete(k)
		return true
	})
	for ticker, info := range divs {
		f.cache.Store(ticker, info)
	}
	f.lastFetch = time.Now()

	if f.logger != nil {
		f.logger.Infof("dividends: fetched %d entries from SmartLab", len(divs))
	}

	return divs, nil
}

// GetForTicker возвращает данные о дивиденде для тикера из кэша.
func (f *Fetcher) GetForTicker(ticker string) (DividendInfo, bool) {
	val, ok := f.cache.Load(ticker)
	if !ok {
		return DividendInfo{}, false
	}
	return val.(DividendInfo), true
}

// fromCache собирает все записи из sync.Map в обычный map.
func (f *Fetcher) fromCache() map[string]DividendInfo {
	result := make(map[string]DividendInfo)
	f.cache.Range(func(k, v any) bool {
		result[k.(string)] = v.(DividendInfo)
		return true
	})
	return result
}

// FilterByLookahead оставляет только дивиденды с ExDivDate в будущем в пределах lookaheadDays дней.
// Записи с нулевым ExDivDate исключаются.
func FilterByLookahead(divs map[string]DividendInfo, lookaheadDays int) map[string]DividendInfo {
	result := make(map[string]DividendInfo)
	now := time.Now()
	horizon := now.AddDate(0, 0, lookaheadDays)

	for ticker, info := range divs {
		if info.ExDivDate.IsZero() {
			continue
		}
		if info.ExDivDate.After(now) && !info.ExDivDate.After(horizon) {
			result[ticker] = info
		}
	}
	return result
}

// parseHTML парсит HTML-страницу SmartLab и возвращает map[ticker]DividendInfo.
func parseHTML(r io.Reader) (map[string]DividendInfo, error) {
	doc, err := html.Parse(r)
	if err != nil {
		return nil, fmt.Errorf("html parse: %w", err)
	}
	return parseDoc(doc), nil
}

// parseDoc обходит HTML-дерево и ищет нужную таблицу.
func parseDoc(doc *html.Node) map[string]DividendInfo {
	result := make(map[string]DividendInfo)
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "table" {
			if hasClass(n, "trades-table") {
				parseTable(n, result)
				return
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)
	return result
}

// parseTable обходит строки tbody таблицы и извлекает данные.
func parseTable(table *html.Node, result map[string]DividendInfo) {
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "tbody" {
			parseBody(n, result)
			return
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(table)
}

// parseBody обрабатывает строки tbody.
func parseBody(tbody *html.Node, result map[string]DividendInfo) {
	for row := tbody.FirstChild; row != nil; row = row.NextSibling {
		if row.Type != html.ElementNode || row.Data != "tr" {
			continue
		}

		status := "forecast"
		if hasClass(row, "dividend_approved") {
			status = "approved"
		}

		cells := collectCells(row)
		if len(cells) < 9 {
			continue
		}

		companyName := cellText(cells[0])
		ticker := strings.TrimSpace(cellText(cells[1]))
		if ticker == "" {
			continue
		}

		dividendRub, _ := parseDecimal(cellText(cells[3]))
		yieldPct, _ := parseYield(cellText(cells[4]))

		exDivDate, _ := parseDate(cellText(cells[6]))
		recordDate, _ := parseDate(cellText(cells[7]))

		result[ticker] = DividendInfo{
			Ticker:      ticker,
			CompanyName: companyName,
			DividendRub: dividendRub,
			YieldPct:    yieldPct,
			ExDivDate:   exDivDate,
			RecordDate:  recordDate,
			Status:      status,
		}
	}
}

// collectCells собирает все <td> из строки <tr>.
func collectCells(row *html.Node) []*html.Node {
	var cells []*html.Node
	for c := row.FirstChild; c != nil; c = c.NextSibling {
		if c.Type == html.ElementNode && c.Data == "td" {
			cells = append(cells, c)
		}
	}
	return cells
}

// cellText рекурсивно собирает текстовое содержимое узла, пропуская &nbsp;.
func cellText(n *html.Node) string {
	var sb strings.Builder
	var walk func(*html.Node)
	walk = func(node *html.Node) {
		if node.Type == html.TextNode {
			text := node.Data
			// &nbsp; парсится как \u00a0
			text = strings.ReplaceAll(text, "\u00a0", "")
			sb.WriteString(text)
		}
		for c := node.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return strings.TrimSpace(sb.String())
}

// hasClass проверяет наличие CSS-класса у узла.
func hasClass(n *html.Node, class string) bool {
	for _, attr := range n.Attr {
		if attr.Key == "class" {
			for _, c := range strings.Fields(attr.Val) {
				if c == class {
					return true
				}
			}
		}
	}
	return false
}

// parseDate парсит дату в формате dd.mm.yyyy.
func parseDate(s string) (time.Time, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}, fmt.Errorf("empty date string")
	}
	t, err := time.Parse("02.01.2006", s)
	if err != nil {
		return time.Time{}, fmt.Errorf("parse date %q: %w", s, err)
	}
	return t, nil
}

// parseDecimal парсит число с запятой в качестве десятичного разделителя.
func parseDecimal(s string) (float64, error) {
	s = strings.TrimSpace(s)
	s = strings.ReplaceAll(s, ",", ".")
	return strconv.ParseFloat(s, 64)
}

// parseYield парсит доходность вида "8,2%" → 8.2.
func parseYield(s string) (float64, error) {
	s = strings.TrimSpace(s)
	s = strings.TrimSuffix(s, "%")
	return parseDecimal(s)
}
