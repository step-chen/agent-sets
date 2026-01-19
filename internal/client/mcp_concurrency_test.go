package client

import (
	"context"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"pr-review-automation/internal/config"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// MockRoundTripper implements http.RoundTripper
type MockRoundTripper struct {
	RoundTripFunc func(req *http.Request) (*http.Response, error)
}

func (m *MockRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	return m.RoundTripFunc(req)
}

func TestMCPClient_Concurrency(t *testing.T) {
	cfg := &config.Config{}
	cfg.MCP.Bitbucket.Endpoint = "http://mock-bitbucket"
	cfg.MCP.Bitbucket.Token = "mock-token"
	cfg.MCP.Retry.Backoff = time.Millisecond
	cfg.MCP.Retry.MaxBackoff = time.Millisecond * 10

	client := NewMCPClient(cfg)

	// Counter for transport creation to verify singleflight
	var transportCount int32

	// Mock factory that sleeps to simulate network latency
	client.transportFactory = func(_ context.Context, endpoint, token string) (mcp.Transport, error) {
		time.Sleep(100 * time.Millisecond) // Simulate latency
		atomic.AddInt32(&transportCount, 1)

		// Return a real transport but with a dummy HTTP client
		// connections will fail, but singleflight logic should still hold
		return &mcp.SSEClientTransport{
			Endpoint: endpoint,
			HTTPClient: &http.Client{
				Transport: &MockRoundTripper{
					RoundTripFunc: func(req *http.Request) (*http.Response, error) {
						// Fail connection immediately (or hang if we wanted)
						return nil, context.Canceled // Just verify creation
					},
				},
			},
		}, nil
	}

	client.endpoints["bitbucket"] = endpointInfo{endpoint: "http://mock", token: "token"}
	client.stale["bitbucket"] = true // Force reconnect

	const concurrency = 20
	var wg sync.WaitGroup
	wg.Add(concurrency)

	start := time.Now()
	for i := 0; i < concurrency; i++ {
		go func() {
			defer wg.Done()
			// This calls getOrReconnect -> reconnect (since stale=true)
			// Using RetryToolset to trigger the flow naturally as used in real code
			_, _ = client.getOrReconnect("bitbucket")
			// We expect errors because MockHTTP fails, but we care about transportCount
		}()
	}

	wg.Wait()
	duration := time.Since(start)

	t.Logf("Concurrent connections took %v", duration)

	// Verification
	count := atomic.LoadInt32(&transportCount)
	if count != 1 {
		t.Errorf("Expected exactly 1 transport creation, got %d. Singleflight failed?", count)
	}

	// Double check map consistency - session might be nil if connect failed, but that's fine.
	// The Singleflight mechanism is what we are testing.
}

func TestMCPClient_ContextCancellation(t *testing.T) {
	// Verify that backoff respects context
	cfg := &config.Config{}
	cfg.MCP.Retry.Backoff = 1 * time.Hour
	cfg.MCP.Retry.MaxBackoff = 1 * time.Hour
	client := &MCPClient{
		cfg: cfg,
	}

	ctx, cancel := context.WithCancel(context.Background())
	start := time.Now()

	go func() {
		time.Sleep(100 * time.Millisecond)
		cancel()
	}()

	// Should return quickly (~100ms) not 1 hour
	client.backoff(ctx, 0)

	if time.Since(start) > 2*time.Second {
		t.Error("Backoff did not respect context cancellation")
	}
}

func TestMCPClient_CircuitBreaker(t *testing.T) {
	cfg := &config.Config{}
	cfg.MCP.Retry.Backoff = time.Millisecond
	cfg.MCP.Retry.MaxBackoff = time.Millisecond * 10

	client := NewMCPClient(cfg)
	client.endpoints["test"] = endpointInfo{endpoint: "http://fake", token: ""}

	// Use a factory that always fails
	client.transportFactory = func(_ context.Context, endpoint, token string) (mcp.Transport, error) {
		return nil, context.DeadlineExceeded
	}

	// First 3 failures should update circuit state
	for i := range 3 {
		_, err := client.getOrReconnect("test")
		if err == nil {
			t.Fatalf("Expected error on attempt %d", i+1)
		}
	}

	// Check circuit is now open
	client.mu.RLock()
	circuit := client.circuits["test"]
	client.mu.RUnlock()

	if circuit == nil {
		t.Fatal("Circuit state should exist")
	}
	if circuit.failures < circuitFailureThreshold {
		t.Errorf("Expected at least %d failures, got %d", circuitFailureThreshold, circuit.failures)
	}
	if !circuit.isOpen() {
		t.Error("Circuit should be open after threshold failures")
	}

	// Next call should be rejected immediately by circuit breaker
	start := time.Now()
	_, err := client.getOrReconnect("test")
	elapsed := time.Since(start)

	if err == nil {
		t.Error("Expected circuit breaker rejection")
	}
	if elapsed > 10*time.Millisecond {
		t.Errorf("Circuit breaker should reject fast, took %v", elapsed)
	}
}
