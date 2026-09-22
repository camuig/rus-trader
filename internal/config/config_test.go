package config

import "testing"

// validConfig returns the minimum that passes Validate, so each test can vary one field.
func validConfig() *Config {
	return &Config{
		Tinkoff: TinkoffConfig{Token: "t.token"},
		LLM:     LLMConfig{APIKey: "sk-key", Model: "deepseek/deepseek-v4-pro"},
		Trading: TradingConfig{Interval: "15m", StopWatchdogInterval: "2m"},
	}
}

func TestValidateWebAuth(t *testing.T) {
	tests := []struct {
		name, user, pass string
		wantErr          bool
	}{
		{"auth disabled", "", "", false},
		{"auth fully configured", "admin", "s3cret", false},
		{"user without password", "admin", "", true},
		{"password without user", "", "s3cret", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := validConfig()
			cfg.Web.AuthUser = tt.user
			cfg.Web.AuthPassword = tt.pass

			err := cfg.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestLLMDefaults(t *testing.T) {
	cfg := &Config{}
	setDefaults(cfg)

	if cfg.LLM.BaseURL != DefaultLLMBaseURL {
		t.Errorf("BaseURL = %q, want %q", cfg.LLM.BaseURL, DefaultLLMBaseURL)
	}
	if cfg.LLM.Model != DefaultLLMModel {
		t.Errorf("Model = %q, want %q", cfg.LLM.Model, DefaultLLMModel)
	}
	if cfg.Sentiment.Model != DefaultLLMCheapModel {
		t.Errorf("Sentiment.Model = %q, want %q", cfg.Sentiment.Model, DefaultLLMCheapModel)
	}
	if cfg.LLM.TimeoutSeconds != DefaultLLMTimeoutSeconds {
		t.Errorf("TimeoutSeconds = %d, want %d", cfg.LLM.TimeoutSeconds, DefaultLLMTimeoutSeconds)
	}
}

func TestMigrateLegacyDeepSeek(t *testing.T) {
	t.Run("legacy section moves to llm with deepseek base_url", func(t *testing.T) {
		cfg := &Config{DeepSeek: &LLMConfig{APIKey: "sk-ds", Model: "deepseek-reasoner", TimeoutSeconds: 300}}
		migrateLegacyDeepSeek(cfg)
		setDefaults(cfg)

		if cfg.DeepSeek != nil {
			t.Error("legacy section should be cleared after migration")
		}
		if cfg.LLM.APIKey != "sk-ds" || cfg.LLM.Model != "deepseek-reasoner" || cfg.LLM.TimeoutSeconds != 300 {
			t.Errorf("legacy values not carried over: %+v", cfg.LLM)
		}
		if cfg.LLM.BaseURL != legacyDeepSeekBaseURL {
			t.Errorf("BaseURL = %q, want legacy %q", cfg.LLM.BaseURL, legacyDeepSeekBaseURL)
		}
	})

	t.Run("llm section wins over legacy", func(t *testing.T) {
		cfg := &Config{
			LLM:      LLMConfig{APIKey: "sk-or"},
			DeepSeek: &LLMConfig{APIKey: "sk-ds", Model: "deepseek-reasoner"},
		}
		migrateLegacyDeepSeek(cfg)
		setDefaults(cfg)

		if cfg.LLM.APIKey != "sk-or" {
			t.Errorf("APIKey = %q, want llm section value", cfg.LLM.APIKey)
		}
		if cfg.LLM.BaseURL != DefaultLLMBaseURL || cfg.LLM.Model != DefaultLLMModel {
			t.Errorf("defaults not applied: %+v", cfg.LLM)
		}
	})

	t.Run("no legacy section is a no-op", func(t *testing.T) {
		cfg := &Config{LLM: LLMConfig{APIKey: "sk-or"}}
		migrateLegacyDeepSeek(cfg)
		if cfg.LLM.APIKey != "sk-or" {
			t.Errorf("APIKey changed: %q", cfg.LLM.APIKey)
		}
	})
}

func TestValidateLLM(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*Config)
		wantErr bool
	}{
		{"valid", func(*Config) {}, false},
		{"missing api key", func(c *Config) { c.LLM.APIKey = "" }, true},
		{"missing model", func(c *Config) { c.LLM.Model = "" }, true},
		{"reasoning low", func(c *Config) { c.LLM.ReasoningEffort = "low" }, false},
		{"reasoning high", func(c *Config) { c.LLM.ReasoningEffort = "high" }, false},
		{"reasoning invalid", func(c *Config) { c.LLM.ReasoningEffort = "max" }, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := validConfig()
			tt.mutate(cfg)
			err := cfg.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
