package main

import (
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/camuig/rus-trader/internal/backtest"
	"github.com/camuig/rus-trader/internal/config"
	"github.com/camuig/rus-trader/internal/logger"
	"github.com/camuig/rus-trader/internal/storage"
)

func main() {
	configPath := flag.String("config", "config.yaml", "path to config file")
	dbPath := flag.String("db", "data/rus-trader.db", "path to SQLite database")
	fromStr := flag.String("from", "", "start date (YYYY-MM-DD), default: 30 days ago")
	toStr := flag.String("to", "", "end date (YYYY-MM-DD), default: today")
	jsonOutput := flag.Bool("json", false, "output as JSON")

	// Parameter overrides
	minConf := flag.Int("min-confidence", 0, "override MinConfidence (0=use config)")
	minRR := flag.Float64("min-rr", 0, "override MinRiskRewardRatio (0=use config)")
	minSL := flag.Float64("min-sl", 0, "override MinStopLossPct (0=use config)")
	minTP := flag.Float64("min-tp", 0, "override MinTakeProfitPct (0=use config)")
	maxDailyLoss := flag.Float64("max-daily-loss", 0, "override MaxDailyLossRub (0=use config)")
	requireUptrend := flag.String("require-uptrend", "", "override RequireUptrend (true/false, empty=use config)")
	minATR := flag.Float64("min-atr", 0, "override MinATRPct (0=use config)")

	flag.Parse()

	cfg, err := config.Load(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "config error: %v\n", err)
		os.Exit(1)
	}

	log := logger.New("error") // quiet

	db, err := storage.NewDatabase(*dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "database error: %v\n", err)
		os.Exit(1)
	}
	repo := storage.NewRepository(db)

	// Parse dates
	now := time.Now()
	from := now.AddDate(0, 0, -30)
	to := now

	if *fromStr != "" {
		from, err = time.Parse("2006-01-02", *fromStr)
		if err != nil {
			fmt.Fprintf(os.Stderr, "invalid --from date: %v\n", err)
			os.Exit(1)
		}
	}
	if *toStr != "" {
		to, err = time.Parse("2006-01-02", *toStr)
		if err != nil {
			fmt.Fprintf(os.Stderr, "invalid --to date: %v\n", err)
			os.Exit(1)
		}
		to = to.Add(23*time.Hour + 59*time.Minute + 59*time.Second)
	}

	// Build overrides
	overrides := backtest.Overrides{}
	if *minConf > 0 {
		overrides.MinConfidence = minConf
	}
	if *minRR > 0 {
		overrides.MinRiskRewardRatio = minRR
	}
	if *minSL > 0 {
		overrides.MinStopLossPct = minSL
	}
	if *minTP > 0 {
		overrides.MinTakeProfitPct = minTP
	}
	if *maxDailyLoss > 0 {
		overrides.MaxDailyLossRub = maxDailyLoss
	}
	if *requireUptrend != "" {
		v := *requireUptrend == "true"
		overrides.RequireUptrend = &v
	}
	if *minATR > 0 {
		overrides.MinATRPct = minATR
	}

	engine := backtest.NewEngine(repo, cfg, log)
	result, err := engine.Run(from, to, overrides)
	if err != nil {
		fmt.Fprintf(os.Stderr, "backtest error: %v\n", err)
		os.Exit(1)
	}

	if result.Real.Trades == 0 && result.Test.Trades == 0 {
		fmt.Println("No trades found in period.")
		return
	}

	if *jsonOutput {
		data, err := backtest.FormatJSON(result)
		if err != nil {
			fmt.Fprintf(os.Stderr, "json error: %v\n", err)
			os.Exit(1)
		}
		fmt.Println(string(data))
	} else {
		fmt.Print(backtest.FormatText(result))
	}
}
