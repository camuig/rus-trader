package scheduler

import (
	"path/filepath"
	"testing"

	"github.com/camuig/rus-trader/internal/broker"
	"github.com/camuig/rus-trader/internal/config"
	"github.com/camuig/rus-trader/internal/logger"
	"github.com/camuig/rus-trader/internal/storage"
	"github.com/camuig/rus-trader/internal/telegram"
)

func newTestSchedulerForReconcile(t *testing.T, lotSize int32) (*Scheduler, *storage.Repository) {
	t.Helper()

	dbPath := filepath.Join(t.TempDir(), "reconcile-test.db")
	db, err := storage.NewDatabase(dbPath)
	if err != nil {
		t.Fatalf("create test database: %v", err)
	}

	repo := storage.NewRepository(db)
	cfg := &config.Config{}
	log := logger.New("error")
	notifier := telegram.NewNotifier(cfg, log)

	return &Scheduler{
		repo:      repo,
		notifier:  notifier,
		logger:    log,
		config:    cfg,
		lotSizeFn: func(uid string) (int32, error) { return lotSize, nil },
	}, repo
}

func TestAdoptOrphanPositions_CreatesRecordForBrokerPosition(t *testing.T) {
	// TTLK лот = 10000 акций; 13118000 шт → 1311 лотов (integer division).
	s, repo := newTestSchedulerForReconcile(t, 10000)

	portfolio := &broker.PortfolioInfo{
		Positions: []broker.PositionInfo{
			{Ticker: "TTLK", InstrumentUID: "uid-ttlk", Quantity: 13118000, AvgPrice: 0.752, CurrentPrice: 0.755},
		},
	}

	s.adoptOrphanPositions(portfolio)

	trade, err := repo.GetOpenTradeByTicker("TTLK")
	if err != nil || trade == nil {
		t.Fatalf("expected adopted trade for TTLK, got err=%v trade=%v", err, trade)
	}
	if trade.Action != "BUY" {
		t.Errorf("expected Action=BUY, got %q", trade.Action)
	}
	if trade.Price != 0.752 {
		t.Errorf("expected Price=0.752, got %v", trade.Price)
	}
	if trade.Quantity != 1311 {
		t.Errorf("expected Quantity=1311 lots (13118000 shares / 10000 lot size), got %d", trade.Quantity)
	}
	if trade.Status != "open" {
		t.Errorf("expected Status=open, got %q", trade.Status)
	}
	if trade.Reasoning == "" {
		t.Errorf("expected non-empty Reasoning")
	}
}

func TestAdoptOrphanPositions_FixesPriorSharesAsLotsRecord(t *testing.T) {
	// Сценарий: предыдущая версия adopt сохранила количество акций вместо лотов.
	// Новая версия должна это исправить, обнаружив несоответствие.
	s, repo := newTestSchedulerForReconcile(t, 10000)

	brokenTrade := &storage.Trade{
		Ticker:    "TTLK",
		Action:    "BUY",
		Price:     0.752,
		Quantity:  13118000, // акции вместо лотов — баг старой версии
		Status:    "open",
		Reasoning: "auto-adopt: позиция найдена у брокера без записи в БД",
	}
	if err := repo.SaveTrade(brokenTrade); err != nil {
		t.Fatalf("save broken trade: %v", err)
	}

	portfolio := &broker.PortfolioInfo{
		Positions: []broker.PositionInfo{
			{Ticker: "TTLK", InstrumentUID: "uid-ttlk", Quantity: 13118000, AvgPrice: 0.752},
		},
	}

	s.adoptOrphanPositions(portfolio)

	trade, err := repo.GetOpenTradeByTicker("TTLK")
	if err != nil || trade == nil {
		t.Fatalf("expected fixed trade for TTLK, got err=%v", err)
	}
	if trade.Quantity != 1311 {
		t.Errorf("expected Quantity to be fixed to 1311 lots, got %d", trade.Quantity)
	}
}

func TestAdoptOrphanPositions_DoesNotFixManuallyCreatedRecord(t *testing.T) {
	// Обычная сделка (не auto-adopt) — в неё не лезем, даже если Quantity кажется странным.
	s, repo := newTestSchedulerForReconcile(t, 10000)

	manualTrade := &storage.Trade{
		Ticker:    "TTLK",
		Action:    "BUY",
		Price:     0.700,
		Quantity:  100,
		Status:    "open",
		Reasoning: "trend breakout on high volume",
	}
	if err := repo.SaveTrade(manualTrade); err != nil {
		t.Fatalf("save manual trade: %v", err)
	}

	portfolio := &broker.PortfolioInfo{
		Positions: []broker.PositionInfo{
			{Ticker: "TTLK", InstrumentUID: "uid-ttlk", Quantity: 13118000, AvgPrice: 0.752},
		},
	}

	s.adoptOrphanPositions(portfolio)

	trade, err := repo.GetOpenTradeByTicker("TTLK")
	if err != nil || trade == nil {
		t.Fatalf("expected existing trade, got err=%v", err)
	}
	if trade.Quantity != 100 || trade.Price != 0.700 {
		t.Errorf("manual trade must not be touched, got Quantity=%d Price=%v", trade.Quantity, trade.Price)
	}
}

func TestAdoptOrphanPositions_SkipsZeroQuantity(t *testing.T) {
	s, repo := newTestSchedulerForReconcile(t, 10000)

	portfolio := &broker.PortfolioInfo{
		Positions: []broker.PositionInfo{
			{Ticker: "TTLK", InstrumentUID: "uid-ttlk", Quantity: 0, AvgPrice: 0.752},
			{Ticker: "", InstrumentUID: "uid-empty", Quantity: 10000, AvgPrice: 1.0},
		},
	}

	s.adoptOrphanPositions(portfolio)

	trades, err := repo.GetOpenTrades()
	if err != nil {
		t.Fatalf("GetOpenTrades: %v", err)
	}
	if len(trades) != 0 {
		t.Fatalf("expected no adoptions, got %d", len(trades))
	}
}
