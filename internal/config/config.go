package config

import (
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Tinkoff  TinkoffConfig  `yaml:"tinkoff"`
	DeepSeek DeepSeekConfig `yaml:"deepseek"`
	Trading  TradingConfig  `yaml:"trading"`
	Telegram TelegramConfig `yaml:"telegram"`
	Web      WebConfig      `yaml:"web"`
	Logging  LoggingConfig  `yaml:"logging"`
}

type TinkoffConfig struct {
	Token     string `yaml:"token"`
	Sandbox   bool   `yaml:"sandbox"`
	AccountID string `yaml:"account_id"`
}

type DeepSeekConfig struct {
	APIKey         string `yaml:"api_key"`
	Model          string `yaml:"model"`
	TimeoutSeconds int    `yaml:"timeout_seconds"`
}

type TradingConfig struct {
	Interval             string  `yaml:"interval"`
	MaxPositionRub       float64 `yaml:"max_position_rub"`
	MinConfidence        int     `yaml:"min_confidence"`
	DefaultStopLossPct   float64 `yaml:"default_stop_loss_pct"`
	DefaultTakeProfitPct float64 `yaml:"default_take_profit_pct"`
	CandleConcurrency    int     `yaml:"candle_concurrency"`
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
		cfg.DeepSeek.TimeoutSeconds = 120
	}
	if cfg.Trading.Interval == "" {
		cfg.Trading.Interval = "15m"
	}
	if cfg.Trading.MaxPositionRub == 0 {
		cfg.Trading.MaxPositionRub = 10000
	}
	if cfg.Trading.MinConfidence == 0 {
		cfg.Trading.MinConfidence = 70
	}
	if cfg.Trading.DefaultStopLossPct == 0 {
		cfg.Trading.DefaultStopLossPct = 3.0
	}
	if cfg.Trading.DefaultTakeProfitPct == 0 {
		cfg.Trading.DefaultTakeProfitPct = 5.0
	}
	if cfg.Trading.CandleConcurrency == 0 {
		cfg.Trading.CandleConcurrency = 10
	}
	if cfg.Web.Port == 0 {
		cfg.Web.Port = 8080
	}
	if cfg.Logging.Level == "" {
		cfg.Logging.Level = "info"
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
