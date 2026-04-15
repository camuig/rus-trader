package vectordb

import (
	"context"
	"fmt"

	openai "github.com/sashabaranov/go-openai"

	"github.com/camuig/rus-trader/internal/logger"
)

type EmbeddingClient struct {
	client *openai.Client
	model  openai.EmbeddingModel
	logger *logger.Logger
}

func NewEmbeddingClient(apiKey, model string, log *logger.Logger) *EmbeddingClient {
	cfg := openai.DefaultConfig(apiKey)
	cfg.BaseURL = "https://openrouter.ai/api/v1"
	return &EmbeddingClient{
		client: openai.NewClientWithConfig(cfg),
		model:  openai.EmbeddingModel(model),
		logger: log,
	}
}

func (c *EmbeddingClient) Embed(ctx context.Context, text string) ([]float32, error) {
	resp, err := c.client.CreateEmbeddings(ctx, openai.EmbeddingRequest{
		Input: []string{text},
		Model: c.model,
	})
	if err != nil {
		return nil, fmt.Errorf("openrouter embedding: %w", err)
	}
	if len(resp.Data) == 0 {
		return nil, fmt.Errorf("openrouter returned empty embedding")
	}
	return resp.Data[0].Embedding, nil
}
