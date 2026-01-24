package llm

import (
	"context"

	"github.com/openai/openai-go"
)

// Client defines the interface for interacting with an LLM provider using OpenAI-compatible types.
type Client interface {
	// Chat sends a chat completion request.
	Chat(ctx context.Context, params openai.ChatCompletionNewParams) (*openai.ChatCompletion, error)
	// SimpleTextQuery sends a simple text query.
	SimpleTextQuery(ctx context.Context, systemPrompt, userInput string) (string, error)
}
