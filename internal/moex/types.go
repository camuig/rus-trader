package moex

import "time"

type MarketTicker struct {
	Ticker    string
	ValToday  float64 // оборот в рублях за день
	LastPrice float64
}

type NewsItem struct {
	ID        int64
	Title     string
	Published time.Time
}
