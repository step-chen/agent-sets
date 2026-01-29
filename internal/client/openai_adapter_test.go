package client

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
)

// TestOpenAIAdapter_Concurrency_Serialization verifies that requests are serialized
// when concurrency limit is 1.
func TestOpenAIAdapter_Concurrency_Serialization(t *testing.T) {
	// 1. Mock Client that sleeps
	calls := int32(0)

	mockHandler := func(req *http.Request) (*http.Response, error) {
		atomic.AddInt32(&calls, 1)
		time.Sleep(100 * time.Millisecond) // Simulated processing
		return &http.Response{
			StatusCode: 200,
			Body:       http.NoBody,
		}, nil
	}

	mockClient := openai.NewClient(option.WithHTTPClient(&http.Client{
		Transport: &roundTripperFunc{mockHandler},
	}))

	// 2. Adapter with limit 1
	adapter := NewOpenAIAdapterWithConfig(&mockClient, "test-model", "http://test", "key", 1)

	// 3. Concurrent Requests
	var wg sync.WaitGroup
	start := time.Now()

	// Launch 2 requests.
	// Req 1 takes 100ms.
	// Req 2 should wait 100ms then take 100ms.
	// Total > 200ms.

	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			// We cheat and pass empty params because our mock handler ignores body
			// But the openai client validates params?
			// The `Chat.Completions.New` validates messages.
			params := openai.ChatCompletionNewParams{
				Messages: []openai.ChatCompletionMessageParamUnion{
					openai.UserMessage("hello"),
				},
			}
			_, _ = adapter.Chat(context.Background(), params)
		}()
	}

	wg.Wait()
	duration := time.Since(start)

	if duration < 200*time.Millisecond {
		t.Errorf("Expected serialization (duration > 200ms), got %v", duration)
	} else {
		t.Logf("Success: Duration %v indicates serialization occurred", duration)
	}
}

// TestOpenAIAdapter_Concurrency_Timeout verifies that a queued request respects context timeout.
func TestOpenAIAdapter_Concurrency_Timeout(t *testing.T) {
	// 1. Mock Client blocks forever
	mockHandler := func(req *http.Request) (*http.Response, error) {
		select {} // Block forever
	}
	mockClient := openai.NewClient(option.WithHTTPClient(&http.Client{
		Transport: &roundTripperFunc{mockHandler},
	}))

	// Adapter limit 1
	adapter := NewOpenAIAdapterWithConfig(&mockClient, "test", "http://test", "key", 1)

	// 2. Start blocking request
	go func() {
		params := openai.ChatCompletionNewParams{
			Messages: []openai.ChatCompletionMessageParamUnion{
				openai.UserMessage("blocker"),
			},
		}
		adapter.Chat(context.Background(), params)
	}()

	// Ensure blocking request holds the lock
	time.Sleep(50 * time.Millisecond)

	// 3. Second request with short timeout
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	start := time.Now()
	params := openai.ChatCompletionNewParams{
		Messages: []openai.ChatCompletionMessageParamUnion{
			openai.UserMessage("victim"),
		},
	}
	_, err := adapter.Chat(ctx, params)

	duration := time.Since(start)

	// It should fail with DeadlineExceeded
	if !errors.Is(err, context.DeadlineExceeded) && !errors.Is(err, context.Canceled) {
		t.Errorf("Expected context deadline exceeded, got: %v", err)
	}

	// It should fail FAST (approx 100ms), not blocked forever.
	if duration > 200*time.Millisecond {
		t.Errorf("Request took too long to timeout: %v", duration)
	} else {
		t.Logf("Success: Timed out in %v", duration)
	}
}

// Helper for mocking HTTP
type roundTripperFunc struct {
	f func(*http.Request) (*http.Response, error)
}

func (r *roundTripperFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return r.f(req)
}
