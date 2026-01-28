package client

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"pr-review-automation/internal/types"

	"github.com/openai/openai-go"
)

// OpenAIAdapter implements llm.Client interface using OpenAI official client
type OpenAIAdapter struct {
	client   *openai.Client
	model    string
	endpoint string
	// ... fields
	apiKey         string
	timeout        time.Duration
	maxConcurrency int
	sem            chan struct{}
}

// NewOpenAIAdapter creates a new OpenAI adapter
func NewOpenAIAdapter(client *openai.Client, model string) *OpenAIAdapter {
	return &OpenAIAdapter{
		client:         client,
		model:          model,
		maxConcurrency: 1,                      // Default to 1
		sem:            make(chan struct{}, 1), // Default semaphore
	}
}

// NewOpenAIAdapterWithConfig creates a new OpenAI adapter with endpoint and API key stored
func NewOpenAIAdapterWithConfig(client *openai.Client, model, endpoint, apiKey string, maxConcurrency int) *OpenAIAdapter {
	semSize := maxConcurrency
	if semSize <= 0 {
		semSize = 1 // Default safe value if 0 passed, though 0 usually means unlimited in some contexts, but here user asked for safety.
		// If 0 means unlimited, we should not init sem.
		// Let's assume 0 means unlimited.
	}

	var sem chan struct{}
	if semSize > 0 {
		sem = make(chan struct{}, semSize)
	}

	return &OpenAIAdapter{
		client:         client,
		model:          model,
		endpoint:       endpoint,
		apiKey:         apiKey,
		timeout:        120 * time.Second, // Default fallback
		maxConcurrency: maxConcurrency,
		sem:            sem,
	}
}

// SetTimeout sets the request timeout
func (a *OpenAIAdapter) SetTimeout(d time.Duration) {
	a.timeout = d
}

// Name returns the model name
func (a *OpenAIAdapter) Name() string {
	return "openai-" + a.model
}

// GetConfig returns the endpoint and API key for external use
func (a *OpenAIAdapter) GetConfig() (endpoint, apiKey string) {
	return a.endpoint, a.apiKey
}

// Ping sends a minimal request to verify connection
func (a *OpenAIAdapter) Ping(ctx context.Context) error {
	slog.Info("checking llm connection...")
	params := openai.ChatCompletionNewParams{
		Model: openai.ChatModel(a.model),
		Messages: []openai.ChatCompletionMessageParamUnion{
			openai.UserMessage("hello"),
		},
		MaxTokens: openai.Int(1),
	}
	_, err := a.client.Chat.Completions.New(ctx, params)
	if err != nil {
		return fmt.Errorf("llm ping failed: %w", err)
	}
	slog.Info("llm connection verified")
	return nil
}

// Chat sends a chat completion request
func (a *OpenAIAdapter) Chat(ctx context.Context, params openai.ChatCompletionNewParams) (*openai.ChatCompletion, error) {
	// Apply configured timeout if valid
	if a.timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, a.timeout)
		defer cancel()
	}

	if a.sem != nil {
		select {
		case a.sem <- struct{}{}:
			defer func() { <-a.sem }()
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}

	// Use default model if not provided
	if params.Model == "" {
		params.Model = openai.ChatModel(a.model)
	}

	resp, err := a.client.Chat.Completions.New(ctx, params)
	if err != nil {
		return nil, a.wrapError(fmt.Errorf("openai request: %w", err))
	}
	return resp, nil
}

// SimpleTextQuery sends a single text request and returns the text response.
// Ideal for simple Q&A like JSON parsing.
func (a *OpenAIAdapter) SimpleTextQuery(ctx context.Context, systemPrompt, userInput string) (string, error) {
	var messages []openai.ChatCompletionMessageParamUnion

	if systemPrompt != "" {
		messages = append(messages, openai.SystemMessage(systemPrompt))
	}
	messages = append(messages, openai.UserMessage(userInput))

	params := openai.ChatCompletionNewParams{
		Model:    openai.ChatModel(a.model),
		Messages: messages,
	}

	resp, err := a.Chat(ctx, params)
	if err != nil {
		return "", err
	}

	if len(resp.Choices) == 0 {
		return "", fmt.Errorf("no openai response")
	}

	return resp.Choices[0].Message.Content, nil
}

// wrapError wraps openai errors into RetryableError if applicable
func (a *OpenAIAdapter) wrapError(err error) error {
	if err == nil {
		return nil
	}

	// Check for openai.APIError
	var apiErr *openai.Error
	if errors.As(err, &apiErr) {
		statusCode := apiErr.StatusCode
		// 429 (Rate Limit) and 5xx (Server Errors) are retryable
		if statusCode == 429 || (statusCode >= 500 && statusCode < 600) {
			return types.NewRetryableError(err)
		}
	}

	return err
}
