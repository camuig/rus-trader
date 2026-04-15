package config

import (
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Tinkoff   TinkoffConfig   `yaml:"tinkoff"`
	DeepSeek  DeepSeekConfig  `yaml:"deepseek"`
	Trading   TradingConfig   `yaml:"trading"`
	Telegram  TelegramConfig  `yaml:"telegram"`
	Web       WebConfig       `yaml:"web"`
	Logging   LoggingConfig   `yaml:"logging"`
	Dividends DividendsConfig `yaml:"dividends"`
	Journal   JournalConfig   `yaml:"journal"`
	AIAgents  AIAgentsConfig  `yaml:"ai_agents"`
}

type TinkoffConfig struct {
	Token     string `yaml:"token"`
	Sandbox   bool   `yaml:"sandbox"`
	AccountID string `yaml:"account_id"`
}

type DeepSeekConfig struct {
	APIKey              string `yaml:"api_key"`
	Model               string `yaml:"model"`
	TimeoutSeconds      int    `yaml:"timeout_seconds"`
	PromptMaxChars      int    `yaml:"prompt_max_chars"`
	MaxTickerBriefChars int    `yaml:"max_ticker_brief_chars"`
	MaxTickerNewsItems  int    `yaml:"max_ticker_news_items"`
	MaxWorldNewsItems   int    `yaml:"max_world_news_items"`
	MaxNewsTitleChars   int    `yaml:"max_news_title_chars"`
}

type TradingConfig struct {
	Interval             string  `yaml:"interval"`
	MaxPositionRub       float64 `yaml:"max_position_rub"`
	MinConfidence        int     `yaml:"min_confidence"`
	DefaultStopLossPct   float64 `yaml:"default_stop_loss_pct"`
	DefaultTakeProfitPct float64 `yaml:"default_take_profit_pct"`
	CandleConcurrency    int     `yaml:"candle_concurrency"`
	CooldownMinutes      int     `yaml:"cooldown_minutes"`
	MinHoldMinutes       int     `yaml:"min_hold_minutes"`
	MaxOpenPositions     int     `yaml:"max_open_positions"`
	MaxDailyTrades       int     `yaml:"max_daily_trades"`
	MaxAnalysisTickers   int     `yaml:"max_analysis_tickers"`
	CommissionPct        float64 `yaml:"commission_pct"`
	MaxSpreadPct         float64 `yaml:"max_spread_pct"`         // max bid/ask spread %, 0=disabled
	TrailingStopEnabled  bool    `yaml:"trailing_stop_enabled"`   // enable trailing stop
	TrailingBreakevenPct float64 `yaml:"trailing_breakeven_pct"`  // % to TP to move SL to breakeven
	TrailingLockProfitPct float64 `yaml:"trailing_lock_profit_pct"` // % to TP to lock 50% profit
	LimitOrderSlippage   float64 `yaml:"limit_order_slippage"`    // % slippage for limit orders, 0=market
	NoLastHourBuy        bool    `yaml:"no_last_hour_buy"`        // block BUY after 17:50 MSK
	MinStopLossPct            float64 `yaml:"min_stop_loss_pct"`              // minimum SL distance from entry (%)
	ATRStopLossMultiplier     float64 `yaml:"atr_sl_multiplier"`              // SL floor = max(min_stop_loss_pct, mult*ATR/price*100)
	MinTakeProfitPct          float64 `yaml:"min_take_profit_pct"`            // minimum TP distance from entry (%)
	MinRiskRewardRatio        float64 `yaml:"min_risk_reward_ratio"`          // minimum TP/SL ratio (e.g. 1.5)
	RequireUptrend            bool    `yaml:"require_uptrend"`                // block BUY when EMA9 < EMA21
	MaxDailyLossRub           float64 `yaml:"max_daily_loss_rub"`             // circuit breaker: max daily loss (RUB), 0=disabled
	MinScreenerScore          float64 `yaml:"min_screener_score"`             // minimum screener score to include ticker (0=disabled)
	MinATRPct                 float64 `yaml:"min_atr_pct"`                    // minimum ATR/price % to allow BUY, 0=disabled
	RecentLossCooldownDays    int     `yaml:"recent_loss_cooldown_days"`      // block BUY on tickers with losses in last N days, 0=disabled
	MaxLosingStreakPerTicker  int     `yaml:"max_losing_streak_per_ticker"`   // block BUY after N consecutive losing trades on same ticker, 0=disabled
	LosingStreakWindowDays    int     `yaml:"losing_streak_window_days"`      // only consider streaks within the last N days, 0=unlimited
}

type TelegramConfig struct {
	Enabled  bool   `yaml:"enabled"`
	BotToken string `yaml:"bot_token"`
	ChatID   int64  `yaml:"chat_id"`
}

type WebConfig struct {
	Port int `yaml:"port"`
}

type LoggingConfig struct {
	Level string `yaml:"level"`
}

type DividendsConfig struct {
	Enabled       bool `yaml:"enabled"`
	CacheTTLHours int  `yaml:"cache_ttl_hours"`
	LookaheadDays int  `yaml:"lookahead_days"`
}

type JournalConfig struct {
	Enabled            bool `yaml:"enabled"`
	MaxLessonsInPrompt int  `yaml:"max_lessons_in_prompt"`
	MaxLessonAgeDays   int  `yaml:"max_lesson_age_days"`
	MinTradesForReview int  `yaml:"min_trades_for_review"`
}

type AIAgentsConfig struct {
	Screening struct {
		MaxChars   int `yaml:"max_chars"`
		MaxTickers int `yaml:"max_tickers"`
	} `yaml:"screening"`
	PositionManager struct {
		MaxChars int `yaml:"max_chars"`
	} `yaml:"position_manager"`
	JournalReview struct {
		MaxChars int    `yaml:"max_chars"`
		Model    string `yaml:"model"`
	} `yaml:"journal_review"`
}

func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config file: %w", err)
	}

	cfg := &Config{}
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}

	setDefaults(cfg)

	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("validate config: %w", err)
	}

	return cfg, nil
}

func setDefaults(cfg *Config) {
	if cfg.DeepSeek.Model == "" {
		cfg.DeepSeek.Model = "deepseek-reasoner"
	}
	if cfg.DeepSeek.TimeoutSeconds == 0 {
		cfg.DeepSeek.TimeoutSeconds = 180
	}
	if cfg.DeepSeek.PromptMaxChars == 0 {
		cfg.DeepSeek.PromptMaxChars = 24000
	}
	if cfg.DeepSeek.MaxTickerBriefChars == 0 {
		cfg.DeepSeek.MaxTickerBriefChars = 900
	}
	if cfg.DeepSeek.MaxTickerNewsItems == 0 {
		cfg.DeepSeek.MaxTickerNewsItems = 8
	}
	if cfg.DeepSeek.MaxWorldNewsItems == 0 {
		cfg.DeepSeek.MaxWorldNewsItems = 5
	}
	if cfg.DeepSeek.MaxNewsTitleChars == 0 {
		cfg.DeepSeek.MaxNewsTitleChars = 120
	}
	if cfg.Trading.Interval == "" {
		cfg.Trading.Interval = "15m"
	}
	if cfg.Trading.MaxPositionRub == 0 {
		cfg.Trading.MaxPositionRub = 10000
	}
	if cfg.Trading.MinConfidence == 0 {
		cfg.Trading.MinConfidence = 75
	}
	if cfg.Trading.DefaultStopLossPct == 0 {
		cfg.Trading.DefaultStopLossPct = 3.0
	}
	if cfg.Trading.DefaultTakeProfitPct == 0 {
		cfg.Trading.DefaultTakeProfitPct = 4.5
	}
	if cfg.Trading.CandleConcurrency == 0 {
		cfg.Trading.CandleConcurrency = 10
	}
	if cfg.Trading.CooldownMinutes == 0 {
		cfg.Trading.CooldownMinutes = 1440
	}
	if cfg.Trading.MinHoldMinutes == 0 {
		cfg.Trading.MinHoldMinutes = 60
	}
	if cfg.Trading.MaxOpenPositions == 0 {
		cfg.Trading.MaxOpenPositions = 5
	}
	if cfg.Trading.MaxDailyTrades == 0 {
		cfg.Trading.MaxDailyTrades = 15
	}
	if cfg.Trading.MaxAnalysisTickers == 0 {
		cfg.Trading.MaxAnalysisTickers = 20
	}
	if cfg.Trading.CommissionPct == 0 {
		cfg.Trading.CommissionPct = 0.025
	}
	if cfg.Trading.MaxSpreadPct == 0 {
		cfg.Trading.MaxSpreadPct = 0.3
	}
	if cfg.Trading.TrailingBreakevenPct == 0 {
		cfg.Trading.TrailingBreakevenPct = 50
	}
	if cfg.Trading.TrailingLockProfitPct == 0 {
		cfg.Trading.TrailingLockProfitPct = 75
	}
	if cfg.Trading.LimitOrderSlippage == 0 {
		cfg.Trading.LimitOrderSlippage = 0.1
	}
	if cfg.Trading.MinStopLossPct == 0 {
		cfg.Trading.MinStopLossPct = 2.0
	}
	if cfg.Trading.ATRStopLossMultiplier == 0 {
		cfg.Trading.ATRStopLossMultiplier = 1.5
	}
	if cfg.Trading.MinTakeProfitPct == 0 {
		cfg.Trading.MinTakeProfitPct = 2.5
	}
	if cfg.Trading.MinRiskRewardRatio == 0 {
		cfg.Trading.MinRiskRewardRatio = 1.5
	}
	if cfg.Trading.MaxDailyLossRub == 0 {
		cfg.Trading.MaxDailyLossRub = 1500
	}
	if cfg.Trading.MinScreenerScore == 0 {
		cfg.Trading.MinScreenerScore = 2
	}
	if cfg.Trading.MinATRPct == 0 {
		cfg.Trading.MinATRPct = 1.2
	}
	if cfg.Trading.RecentLossCooldownDays == 0 {
		cfg.Trading.RecentLossCooldownDays = 1
	}
	if cfg.Trading.MaxLosingStreakPerTicker == 0 {
		cfg.Trading.MaxLosingStreakPerTicker = 2
	}
	if cfg.Trading.LosingStreakWindowDays == 0 {
		cfg.Trading.LosingStreakWindowDays = 7
	}
	if cfg.Web.Port == 0 {
		cfg.Web.Port = 8080
	}
	if cfg.Logging.Level == "" {
		cfg.Logging.Level = "info"
	}
	if cfg.Dividends.CacheTTLHours == 0 {
		cfg.Dividends.CacheTTLHours = 12
	}
	if cfg.Dividends.LookaheadDays == 0 {
		cfg.Dividends.LookaheadDays = 30
	}
	if cfg.Journal.MaxLessonsInPrompt == 0 {
		cfg.Journal.MaxLessonsInPrompt = 5
	}
	if cfg.Journal.MaxLessonAgeDays == 0 {
		cfg.Journal.MaxLessonAgeDays = 7
	}
	if cfg.Journal.MinTradesForReview == 0 {
		cfg.Journal.MinTradesForReview = 1
	}
	if cfg.AIAgents.Screening.MaxChars == 0 {
		cfg.AIAgents.Screening.MaxChars = 16000
	}
	if cfg.AIAgents.Screening.MaxTickers == 0 {
		cfg.AIAgents.Screening.MaxTickers = 20
	}
	if cfg.AIAgents.PositionManager.MaxChars == 0 {
		cfg.AIAgents.PositionManager.MaxChars = 8000
	}
	if cfg.AIAgents.JournalReview.MaxChars == 0 {
		cfg.AIAgents.JournalReview.MaxChars = 12000
	}
}

func (c *Config) Validate() error {
	if c.Tinkoff.Token == "" {
		return fmt.Errorf("tinkoff.token is required")
	}
	if c.DeepSeek.APIKey == "" {
		return fmt.Errorf("deepseek.api_key is required")
	}
	if _, err := time.ParseDuration(c.Trading.Interval); err != nil {
		return fmt.Errorf("invalid trading.interval %q: %w", c.Trading.Interval, err)
	}
	if c.Telegram.Enabled {
		if c.Telegram.BotToken == "" {
			return fmt.Errorf("telegram.bot_token is required when telegram is enabled")
		}
		if c.Telegram.ChatID == 0 {
			return fmt.Errorf("telegram.chat_id is required when telegram is enabled")
		}
	}
	return nil
}

func (c *Config) IsSandbox() bool {
	return c.Tinkoff.Sandbox
}

func (c *Config) MOEXLocation() *time.Location {
	loc, err := time.LoadLocation("Europe/Moscow")
	if err != nil {
		loc = time.FixedZone("MSK", 3*60*60)
	}
	return loc
}

func (c *Config) TradingInterval() time.Duration {
	d, _ := time.ParseDuration(c.Trading.Interval)
	return d
}

func (c *Config) DeepSeekTimeout() time.Duration {
	return time.Duration(c.DeepSeek.TimeoutSeconds) * time.Second
}
