package client

import (
	"pr-review-automation/internal/config"
	"time"

	"github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
)

// NewLLM creates a new LLM instance based on configuration
// IMPORTANT: The returned LLM instance is safe for concurrent use from multiple goroutines,
// as long as its configuration (API key, endpoint) is NOT modified after creation.
// This is the standard practice for http.Client based libraries.
func NewLLM(cfg *config.Config) (LLMClient, error) {
	client := openai.NewClient(
		option.WithAPIKey(cfg.LLM.APIKey),
		option.WithBaseURL(cfg.LLM.Endpoint),
	)
	// Use NewOpenAIAdapterWithConfig to ensure endpoint and apiKey are stored for GetConfig()
	// Unified Concurrency: Use Server.ConcurrencyLimit for LLM adapter
	adapter := NewOpenAIAdapterWithConfig(&client, cfg.LLM.Model, cfg.LLM.Endpoint, cfg.LLM.APIKey, int(cfg.Server.ConcurrencyLimit))
	if cfg.LLM.Timeout > 0 {
		adapter.SetTimeout(cfg.LLM.Timeout)
	}
	return adapter, nil
}

// NewLLMFromConfig creates a specific LLM client from config parameters
func NewLLMFromConfig(endpoint, apiKey, model string, timeout time.Duration) LLMClient {
	client := openai.NewClient(
		option.WithAPIKey(apiKey),
		option.WithBaseURL(endpoint),
	)
	// Default concurrency 1 for specific/backup clients
	adapter := NewOpenAIAdapterWithConfig(&client, model, endpoint, apiKey, 1)
	if timeout > 0 {
		adapter.SetTimeout(timeout)
	}
	return adapter
}
