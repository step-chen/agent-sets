package client

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
	"google.golang.org/adk/model"
	"google.golang.org/genai"
)

func TestOpenAIAdapter_GenerateContent(t *testing.T) {
	// Mock OpenAI API Server
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			t.Errorf("Expected path /chat/completions, got %s", r.URL.Path)
		}

		// Parse request to verify conversion
		var reqBody map[string]any
		if err := json.NewDecoder(r.Body).Decode(&reqBody); err != nil {
			t.Errorf("Failed to decode request body: %v", err)
		}

		// Simple response
		w.Header().Set("Content-Type", "application/json")
		response := map[string]any{
			"id":      "chatcmpl-123",
			"object":  "chat.completion",
			"created": 1677652288,
			"model":   "gpt-4o",
			"choices": []map[string]any{
				{
					"index": 0,
					"message": map[string]any{
						"role":    "assistant",
						"content": "Hello, world!",
					},
					"finish_reason": "stop",
				},
			},
			"usage": map[string]any{
				"prompt_tokens":     9,
				"completion_tokens": 12,
				"total_tokens":      21,
			},
		}
		json.NewEncoder(w).Encode(response)
	}))
	defer ts.Close()

	// Initialize Adapter
	client := openai.NewClient(
		option.WithBaseURL(ts.URL),
		option.WithAPIKey("test-key"),
	)
	adapter := &OpenAIAdapter{
		client: &client, // Assuming &client is correct based on factory.go logic
		model:  "gpt-4o",
	}

	// Test Request
	ctx := context.Background()
	req := &model.LLMRequest{
		Contents: []*genai.Content{
			{
				Role: "user",
				Parts: []*genai.Part{
					genai.NewPartFromText("Say hello"),
				},
			},
		},
	}

	// Execute
	iter := adapter.GenerateContent(ctx, req, false)

	var responses []*model.LLMResponse
	for resp, err := range iter {
		if err != nil {
			t.Fatalf("GenerateContent stream error: %v", err)
		}
		responses = append(responses, resp)
	}

	if len(responses) == 0 {
		t.Fatal("Expected at least one response")
	}

	// Verify Content
	firstResp := responses[0]
	if len(firstResp.Content.Parts) == 0 {
		t.Fatal("Expected content parts")
	}

	// Part is struct, access fields directly
	part := firstResp.Content.Parts[0]
	if part.Text != "Hello, world!" {
		t.Errorf("Expected 'Hello, world!', got %s", part.Text)
	}
}

func TestOpenAIAdapter_GenerateContent_Stream(t *testing.T) {
	// Mock OpenAI Streaming Server
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")

		// Send chunks
		chunks := []string{
			`{"id":"1","object":"chat.completion.chunk","created":1,"model":"gpt-4o","choices":[{"index":0,"delta":{"role":"assistant","content":""},"finish_reason":null}]}`,
			`{"id":"1","object":"chat.completion.chunk","created":1,"model":"gpt-4o","choices":[{"index":0,"delta":{"content":"Hello"},"finish_reason":null}]}`,
			`{"id":"1","object":"chat.completion.chunk","created":1,"model":"gpt-4o","choices":[{"index":0,"delta":{"content":" world"},"finish_reason":null}]}`,
			`{"id":"1","object":"chat.completion.chunk","created":1,"model":"gpt-4o","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
			`[DONE]`,
		}

		for _, chunk := range chunks {
			w.Write([]byte("data: " + chunk + "\n\n"))
			w.(http.Flusher).Flush()
		}
	}))
	defer ts.Close()

	client := openai.NewClient(
		option.WithBaseURL(ts.URL),
		option.WithAPIKey("test-key"),
	)
	adapter := &OpenAIAdapter{
		client: &client,
		model:  "gpt-4o",
	}

	ctx := context.Background()
	req := &model.LLMRequest{
		Contents: []*genai.Content{
			{Role: "user", Parts: []*genai.Part{genai.NewPartFromText("Stream hello")}},
		},
	}

	iter := adapter.GenerateContent(ctx, req, true)

	var fullText string
	for resp, err := range iter {
		if err != nil {
			t.Fatalf("Stream error: %v", err)
		}
		if resp.Content != nil {
			for _, p := range resp.Content.Parts {
				fullText += p.Text
			}
		}
	}

	if fullText != "Hello world" {
		t.Errorf("Expected 'Hello world', got '%s'", fullText)
	}
}
