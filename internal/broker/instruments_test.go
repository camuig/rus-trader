package broker

import (
	"testing"
	"time"
)

// При свежей записи в кэше IsAPITradeAvailable возвращает её значение,
// не обращаясь к API (Client здесь nil — обращение к нему вызвало бы панику).
func TestIsAPITradeAvailable_FreshCacheHit(t *testing.T) {
	bc := &BrokerClient{}

	cases := []struct {
		name  string
		uid   string
		value bool
	}{
		{"available", "uid-fresh-true", true},
		{"forbidden", "uid-fresh-false", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			apiTradeAvailCache.Store(tc.uid, apiTradeAvailEntry{
				value:     tc.value,
				expiresAt: time.Now().Add(time.Minute),
			})
			defer apiTradeAvailCache.Delete(tc.uid)

			if got := bc.IsAPITradeAvailable(tc.uid); got != tc.value {
				t.Fatalf("IsAPITradeAvailable = %v, want %v", got, tc.value)
			}
		})
	}
}

// Просроченная запись удаляется из кэша (затем последовал бы запрос к API).
func TestIsAPITradeAvailable_ExpiredEntryEvicted(t *testing.T) {
	const uid = "uid-expired"
	apiTradeAvailCache.Store(uid, apiTradeAvailEntry{
		value:     false,
		expiresAt: time.Now().Add(-time.Minute),
	})

	bc := &BrokerClient{}
	func() {
		// При промахе по TTL код пойдёт в API через nil Client и упадёт —
		// нас интересует только то, что просроченная запись вычищена.
		defer func() { _ = recover() }()
		bc.IsAPITradeAvailable(uid)
	}()

	if _, ok := apiTradeAvailCache.Load(uid); ok {
		t.Fatal("expired entry was not evicted from cache")
	}
}
