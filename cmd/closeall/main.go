package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/camuig/rus-trader/internal/broker"
	"github.com/camuig/rus-trader/internal/config"
	"github.com/camuig/rus-trader/internal/logger"
)

func main() {
	configPath := flag.String("config", "config.yaml", "path to config file")
	dryRun := flag.Bool("dry-run", false, "show positions without closing")
	flag.Parse()

	cfg, err := config.Load(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "config error: %v\n", err)
		os.Exit(1)
	}

	log := logger.New(cfg.Logging.Level)

	ctx := context.Background()
	bc, err := broker.NewBrokerClient(ctx, cfg, log)
	if err != nil {
		fmt.Fprintf(os.Stderr, "broker init error: %v\n", err)
		os.Exit(1)
	}
	defer bc.Stop()

	portfolio, err := bc.GetPortfolio()
	if err != nil {
		fmt.Fprintf(os.Stderr, "get portfolio error: %v\n", err)
		os.Exit(1)
	}

	if len(portfolio.Positions) == 0 {
		fmt.Println("No open positions.")
		return
	}

	fmt.Printf("Found %d position(s):\n\n", len(portfolio.Positions))
	for _, p := range portfolio.Positions {
		fmt.Printf("  %s: %.0f шт, ср.цена %.2f, текущая %.2f, P&L %.2f\n",
			p.Ticker, p.Quantity, p.AvgPrice, p.CurrentPrice, p.PnL)
	}
	fmt.Println()

	if *dryRun {
		fmt.Println("Dry run — no orders placed.")
		return
	}

	var closed, failed int
	for _, p := range portfolio.Positions {
		lots := int64(p.Quantity)
		if lots <= 0 {
			continue
		}

		uid := p.InstrumentUID
		if uid == "" {
			var err error
			uid, err = bc.ResolveTickerToUID(p.Ticker)
			if err != nil {
				fmt.Fprintf(os.Stderr, "  [FAIL] %s: resolve UID: %v\n", p.Ticker, err)
				failed++
				continue
			}
		}

		result, err := bc.Sell(uid, lots)
		if err != nil {
			fmt.Fprintf(os.Stderr, "  [FAIL] %s: sell: %v\n", p.Ticker, err)
			failed++
			continue
		}

		fmt.Printf("  [OK]   %s: sold %d lots @ %.2f\n", p.Ticker, result.ExecutedLots, result.ExecutedPrice)
		closed++
	}

	fmt.Printf("\nDone: %d closed, %d failed.\n", closed, failed)
	if failed > 0 {
		os.Exit(1)
	}
}
