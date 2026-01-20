package client

import (
	"pr-review-automation/internal/config"

	"github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
	"google.golang.org/adk/model"
)

// NewLLM creates a new LLM instance based on configuration
// IMPORTANT: The returned LLM instance is safe for concurrent use from multiple goroutines,
// as long as its configuration (API key, endpoint) is NOT modified after creation.
// This is the standard practice for http.Client based libraries.
func NewLLM(cfg *config.Config) (model.LLM, error) {
	client := openai.NewClient(
		option.WithAPIKey(cfg.LLM.APIKey),
		option.WithBaseURL(cfg.LLM.Endpoint),
	)
	return &OpenAIAdapter{client: &client, model: cfg.LLM.Model}, nil
}
