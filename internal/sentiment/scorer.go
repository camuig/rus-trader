package sentiment

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/camuig/rus-trader/internal/ai"
	"github.com/camuig/rus-trader/internal/config"
	"github.com/camuig/rus-trader/internal/logger"
)

type SentimentResult struct {
	Ticker string  `json:"ticker"`
	Score  float64 `json:"score"`
	Label  string  `json:"-"`
	Reason string  `json:"reason"`
}

type Scorer struct {
	aiClient *ai.DeepSeekClient
	config   *config.Config
	logger   *logger.Logger
}

func NewScorer(aiClient *ai.DeepSeekClient, cfg *config.Config, log *logger.Logger) *Scorer {
	return &Scorer{aiClient: aiClient, config: cfg, logger: log}
}

const sentimentSystemPrompt = `Оцени sentiment по каждому тикеру на основе новостей и мнений трейдеров.
Шкала: -1.0 (крайне негативный) до +1.0 (крайне позитивный). 0 = нейтральный.
Формат: JSON массив.
[{"ticker":"SBER","score":0.6,"reason":"Рекордные дивиденды"}]`

func (s *Scorer) ScoreBatch(ctx context.Context, tickerTexts map[string][]string) (map[string]*SentimentResult, error) {
	if len(tickerTexts) == 0 {
		return nil, nil
	}

	userPrompt := BuildSentimentPrompt(tickerTexts)

	model := s.config.Sentiment.Model
	if model == "" {
		model = "deepseek-chat"
	}

	rawResponse, err := s.aiClient.CallLLM(ctx, sentimentSystemPrompt, userPrompt, model, 60)
	if err != nil {
		return nil, fmt.Errorf("sentiment scoring: %w", err)
	}

	var results []SentimentResult
	cleaned := ai.StripThinkTags(rawResponse)
	if start := strings.Index(cleaned, "["); start >= 0 {
		if end := strings.LastIndex(cleaned, "]"); end > start {
			_ = json.Unmarshal([]byte(cleaned[start:end+1]), &results)
		}
	}

	resultMap := make(map[string]*SentimentResult, len(results))
	for i := range results {
		results[i].Label = LabelFromScore(results[i].Score)
		resultMap[results[i].Ticker] = &results[i]
	}
	return resultMap, nil
}

func BuildSentimentPrompt(tickerTexts map[string][]string) string {
	var sb strings.Builder
	sb.WriteString("Оцени sentiment по каждому тикеру:\n\n")
	for ticker, texts := range tickerTexts {
		sb.WriteString(fmt.Sprintf("%s:\n", ticker))
		for _, t := range texts {
			sb.WriteString(fmt.Sprintf("- %s\n", t))
		}
		sb.WriteString("\n")
	}
	return sb.String()
}

func LabelFromScore(score float64) string {
	switch {
	case score >= 0.6:
		return "very_positive"
	case score >= 0.3:
		return "positive"
	case score <= -0.6:
		return "very_negative"
	case score <= -0.3:
		return "negative"
	default:
		return "neutral"
	}
}
