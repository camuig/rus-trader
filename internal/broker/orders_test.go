package broker

import (
	"math"
	"testing"

	"github.com/russianinvestments/invest-api-go-sdk/investgo"
	pb "github.com/russianinvestments/invest-api-go-sdk/proto"
)

func makeMoneyValue(units int64, nano int32) *pb.MoneyValue {
	return &pb.MoneyValue{Units: units, Nano: nano}
}

func makeOrderResp(orderID string, lots int64, price *pb.MoneyValue) *investgo.PostOrderResponse {
	return &investgo.PostOrderResponse{
		PostOrderResponse: &pb.PostOrderResponse{
			OrderId:            orderID,
			LotsExecuted:       lots,
			ExecutedOrderPrice: price,
		},
	}
}

func TestExtractOrderResult_DividesByShares(t *testing.T) {
	resp := makeOrderResp("ord-1", 646, makeMoneyValue(749812, 200_000_000))

	got := extractOrderResult(resp, 100, 0, nil)

	if got.ExecutedLots != 646 {
		t.Fatalf("ExecutedLots = %d, want 646", got.ExecutedLots)
	}
	want := 749812.2 / float64(646*100)
	if math.Abs(got.ExecutedPrice-want) > 1e-6 {
		t.Fatalf("ExecutedPrice = %v, want %v", got.ExecutedPrice, want)
	}
}

func TestExtractOrderResult_ZeroLots(t *testing.T) {
	resp := makeOrderResp("ord-2", 0, makeMoneyValue(500, 0))

	got := extractOrderResult(resp, 100, 0, nil)

	if got.ExecutedLots != 0 {
		t.Fatalf("ExecutedLots = %d, want 0", got.ExecutedLots)
	}
	if got.ExecutedPrice != 500 {
		t.Fatalf("ExecutedPrice = %v, want fallback to total 500", got.ExecutedPrice)
	}
}

func TestExtractOrderResult_NilPrice(t *testing.T) {
	resp := &investgo.PostOrderResponse{
		PostOrderResponse: &pb.PostOrderResponse{
			OrderId:      "ord-3",
			LotsExecuted: 10,
		},
	}

	got := extractOrderResult(resp, 1, 100, nil)

	if got.ExecutedPrice != 0 {
		t.Fatalf("ExecutedPrice = %v, want 0", got.ExecutedPrice)
	}
}

func TestExtractOrderResult_PerShareMatchesQuote(t *testing.T) {
	// Нормальный случай: total/(lots*lotSize) совпадает с котировкой.
	resp := makeOrderResp("ord-4", 10, makeMoneyValue(9548, 0)) // 10 лотов × 10 акций × 95.48

	got := extractOrderResult(resp, 10, 95.48, nil)

	if math.Abs(got.ExecutedPrice-95.48) > 1e-6 {
		t.Fatalf("ExecutedPrice = %v, want 95.48", got.ExecutedPrice)
	}
}

func TestExtractOrderResult_APIReturnedPerShare(t *testing.T) {
	// Песочница вернула цену за акцию (95.48), деление на 780 акций дало бы 0.12.
	resp := makeOrderResp("ord-5", 78, makeMoneyValue(95, 480_000_000))

	got := extractOrderResult(resp, 10, 95.9, nil)

	if math.Abs(got.ExecutedPrice-95.48) > 1e-6 {
		t.Fatalf("ExecutedPrice = %v, want raw total 95.48 (API returned per-share)", got.ExecutedPrice)
	}
}

func TestExtractOrderResult_AnomalyFallsBackToQuote(t *testing.T) {
	// Обе интерпретации аномальны (кейс EUTR 0.23) — используем котировку.
	resp := makeOrderResp("ord-6", 167, makeMoneyValue(38, 0))

	got := extractOrderResult(resp, 1, 29.85, nil)

	if got.ExecutedPrice != 29.85 {
		t.Fatalf("ExecutedPrice = %v, want quote 29.85", got.ExecutedPrice)
	}
}

func TestExtractOrderResult_ZeroLotSizeUsesQuote(t *testing.T) {
	// GetLotSize упал (lotSize=0): raw = стоимость позиции, спасает котировка.
	resp := makeOrderResp("ord-7", 177, makeMoneyValue(4984, 320_000_000))

	got := extractOrderResult(resp, 0, 28.16, nil)

	if got.ExecutedPrice != 28.16 {
		t.Fatalf("ExecutedPrice = %v, want quote 28.16", got.ExecutedPrice)
	}
}

func TestWithinPct(t *testing.T) {
	cases := []struct {
		a, b, pct float64
		want      bool
	}{
		{100, 100, 20, true},
		{119, 100, 20, true},
		{121, 100, 20, false},
		{0.23, 29.85, 20, false},
		{100, 0, 20, false},
	}
	for _, c := range cases {
		if got := withinPct(c.a, c.b, c.pct); got != c.want {
			t.Errorf("withinPct(%v, %v, %v) = %v, want %v", c.a, c.b, c.pct, got, c.want)
		}
	}
}
