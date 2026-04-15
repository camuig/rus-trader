package ai

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

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

// callLLM executes a streaming chat completion with the given system/user prompts.
func (d *DeepSeekClient) callLLM(ctx context.Context, sysPrompt, userPrompt, model string, timeoutSec int) (string, error) {
	if model == "" {
		model = d.model
	}
	timeout := time.Duration(timeoutSec) * time.Second
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	stream, err := d.client.CreateChatCompletionStream(ctx, openai.ChatCompletionRequest{
		Model: model,
		Messages: []openai.ChatCompletionMessage{
			{Role: openai.ChatMessageRoleSystem, Content: sysPrompt},
			{Role: openai.ChatMessageRoleUser, Content: userPrompt},
		},
	})
	if err != nil {
		return "", fmt.Errorf("create stream: %w", err)
	}
	defer stream.Close()

	var content strings.Builder
	for {
		chunk, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			if content.Len() > 0 {
				break
			}
			return "", fmt.Errorf("stream recv: %w", err)
		}
		if len(chunk.Choices) > 0 {
			content.WriteString(chunk.Choices[0].Delta.Content)
		}
	}
	return content.String(), nil
}

// ScreeningAnalyze calls DeepSeek for BUY candidate screening.
func (d *DeepSeekClient) ScreeningAnalyze(ctx context.Context, req *ScreeningRequest) ([]AIDecision, string, error) {
	userPrompt := BuildScreeningPrompt(req, d.cfg.AIAgents.Screening.MaxChars)

	d.logger.Info("screening agent prompt built",
		"chars", len([]rune(userPrompt)), "tickers", len(req.TickerFeatures))

	rawResponse, err := d.callLLM(ctx, screeningSystemPrompt, userPrompt, d.model, d.cfg.DeepSeek.TimeoutSeconds)
	if err != nil {
		return nil, "", fmt.Errorf("screening agent: %w", err)
	}

	d.logger.Info("screening agent response", "length", len(rawResponse))
	d.logger.Debug("screening raw response", "content", rawResponse)

	decisions, err := ParseDecisions(rawResponse)
	if err != nil {
		return nil, rawResponse, fmt.Errorf("parse screening decisions: %w", err)
	}

	var buys []AIDecision
	for _, dec := range decisions {
		if dec.Action == "BUY" {
			buys = append(buys, dec)
		}
	}
	return buys, rawResponse, nil
}
