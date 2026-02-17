package ai

import (
	"context"
	"fmt"

	openai "github.com/sashabaranov/go-openai"

	"github.com/camuig/rus-trader/internal/config"
	"github.com/camuig/rus-trader/internal/logger"
)

type DeepSeekClient struct {
	client *openai.Client
	model  string
	cfg    *config.Config
	logger *logger.Logger
}

func NewDeepSeekClient(cfg *config.Config, log *logger.Logger) *DeepSeekClient {
	ocfg := openai.DefaultConfig(cfg.DeepSeek.APIKey)
	ocfg.BaseURL = "https://api.deepseek.com/v1"

	return &DeepSeekClient{
		client: openai.NewClientWithConfig(ocfg),
		model:  cfg.DeepSeek.Model,
		cfg:    cfg,
		logger: log,
	}
}

func (d *DeepSeekClient) Analyze(ctx context.Context, req *AnalysisRequest) ([]AIDecision, string, error) {
	ctx, cancel := context.WithTimeout(ctx, d.cfg.DeepSeekTimeout())
	defer cancel()

	userPrompt := BuildUserPrompt(req)

	d.logger.Info("sending analysis request to DeepSeek",
		"tickers", len(req.Tickers),
		"positions", len(req.Positions))

	resp, err := d.client.CreateChatCompletion(ctx, openai.ChatCompletionRequest{
		Model: d.model,
		Messages: []openai.ChatCompletionMessage{
			{Role: openai.ChatMessageRoleSystem, Content: systemPrompt},
			{Role: openai.ChatMessageRoleUser, Content: userPrompt},
		},
	})
	if err != nil {
		return nil, "", fmt.Errorf("deepseek API call: %w", err)
	}

	if len(resp.Choices) == 0 {
		return nil, "", fmt.Errorf("deepseek returned no choices")
	}

	rawResponse := resp.Choices[0].Message.Content
	d.logger.Info("received AI response", "length", len(rawResponse))
	d.logger.Debug("AI raw response", "content", rawResponse)

	decisions, err := ParseDecisions(rawResponse)
	if err != nil {
		return nil, rawResponse, fmt.Errorf("parse AI response: %w", err)
	}

	return decisions, rawResponse, nil
}
