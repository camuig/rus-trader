package ai

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

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

func (d *DeepSeekClient) Analyze(ctx context.Context, req *AnalysisRequest, todayTraded []string) ([]AIDecision, string, error) {
	ctx, cancel := context.WithTimeout(ctx, d.cfg.DeepSeekTimeout())
	defer cancel()

	limits := PromptLimits{
		MaxChars:            d.cfg.DeepSeek.PromptMaxChars,
		MaxTickerBriefChars: d.cfg.DeepSeek.MaxTickerBriefChars,
		MaxTickerNewsItems:  d.cfg.DeepSeek.MaxTickerNewsItems,
		MaxWorldNewsItems:   d.cfg.DeepSeek.MaxWorldNewsItems,
		MaxNewsTitleChars:   d.cfg.DeepSeek.MaxNewsTitleChars,
	}
	userPrompt := BuildUserPrompt(req, todayTraded, limits)

	d.logger.Info("sending analysis request to DeepSeek",
		"tickers", len(req.Tickers),
		"positions", len(req.Positions),
		"prompt_length", len([]rune(userPrompt)))

	stream, err := d.client.CreateChatCompletionStream(ctx, openai.ChatCompletionRequest{
		Model: d.model,
		Messages: []openai.ChatCompletionMessage{
			{Role: openai.ChatMessageRoleSystem, Content: systemPrompt},
			{Role: openai.ChatMessageRoleUser, Content: userPrompt},
		},
	})
	if err != nil {
		return nil, "", fmt.Errorf("deepseek API call: %w", err)
	}
	defer stream.Close()

	var content strings.Builder
	for {
		chunk, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, content.String(), fmt.Errorf("deepseek stream: %w", err)
		}
		if len(chunk.Choices) > 0 {
			content.WriteString(chunk.Choices[0].Delta.Content)
		}
	}

	rawResponse := content.String()
	d.logger.Info("received AI response", "length", len(rawResponse))
	d.logger.Debug("AI raw response", "content", rawResponse)

	decisions, err := ParseDecisions(rawResponse)
	if err != nil {
		return nil, rawResponse, fmt.Errorf("parse AI response: %w", err)
	}

	return decisions, rawResponse, nil
}
