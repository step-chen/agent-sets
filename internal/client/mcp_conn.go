package client

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"pr-review-automation/internal/metrics"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// circuitState represents the state of a circuit breaker for a single MCP server
type circuitState struct {
	failures    int       // Consecutive failure count
	lastFailure time.Time // Time of last failure
	openUntil   time.Time // Circuit open until this time (requests are rejected)
}

// isOpen returns true if the circuit is open and should reject requests
func (cs *circuitState) isOpen() bool {
	if cs.openUntil.IsZero() {
		return false
	}
	return time.Now().Before(cs.openUntil)
}

// endpointInfo stores connection configuration for reconnection
type endpointInfo struct {
	endpoint     string
	token        string
	authHeader   string   // Header name for token
	allowedTools []string // Whitelist of tool names to expose
}

// IsHealthy checks if all configured connections are healthy
func (c *MCPClient) IsHealthy() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()

	for name := range c.endpoints {
		if c.stale[name] {
			return false
		}
		// Check if transport exists
		if _, ok := c.transports[name]; !ok {
			return false
		}
	}
	return true
}

// getOrReconnect returns existing session or reconnects if stale
func (c *MCPClient) getOrReconnect(name string) (*mcp.ClientSession, error) {
	c.mu.RLock()
	logger := slog.With("server", name)
	session, hasSession := c.sessions[name]
	isStale := c.stale[name]
	circuit := c.circuits[name]
	c.mu.RUnlock()

	logger.Debug("get session", "exists", hasSession, "stale", isStale)

	// Circuit breaker check - fast fail if circuit is open
	if circuit != nil && circuit.isOpen() {
		logger.Debug("Circuit breaker open, rejecting request",
			"open_until", circuit.openUntil,
			"failures", circuit.failures)
		metrics.MCPToolCalls.WithLabelValues(name, "circuit_breaker", "rejected").Inc()
		return nil, fmt.Errorf("circuit open: %s, retry after %v", name, time.Until(circuit.openUntil))
	}

	if hasSession && !isStale {
		return session, nil
	}

	// Use singleflight to deduplicate concurrent reconnection attempts
	val, err, _ := c.requestGroup.Do(name, func() (interface{}, error) {
		// Double check inside the singleflight to see if another call just finished it
		c.mu.RLock()
		session, hasSession := c.sessions[name]
		isStale := c.stale[name]
		c.mu.RUnlock()

		if hasSession && !isStale {
			return session, nil
		}

		return c.reconnect(name, logger)
	})

	if err != nil {
		// Update circuit breaker state on failure
		c.recordFailure(name)
		return nil, err
	}
	return val.(*mcp.ClientSession), nil
}

// recordFailure updates circuit breaker state after a connection failure
func (c *MCPClient) recordFailure(name string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	circuit := c.circuits[name]
	if circuit == nil {
		circuit = &circuitState{}
		c.circuits[name] = circuit
	}

	circuit.failures++
	circuit.lastFailure = time.Now()

	// Use configuration for circuit breaker
	threshold := c.cfg.MCP.CircuitBreaker.FailureThreshold
	openDuration := c.cfg.MCP.CircuitBreaker.OpenDuration

	if circuit.failures >= threshold {
		circuit.openUntil = time.Now().Add(openDuration)
		slog.Warn("circuit breaker opened",
			"server", name,
			"failures", circuit.failures,
			"open_until", circuit.openUntil)
		metrics.MCPToolCalls.WithLabelValues(name, "circuit_breaker", "opened").Inc()
	}
}

// forceReconnect forces a reconnection for a server
func (c *MCPClient) forceReconnect(name string) {
	c.mu.Lock()
	c.stale[name] = true
	c.mu.Unlock()
}

// backoff sleeps with exponential backoff, respecting context
func (c *MCPClient) backoff(ctx context.Context, attempt int) {
	backoff := c.cfg.MCP.Retry.Backoff * time.Duration(1<<attempt)
	if backoff > c.cfg.MCP.Retry.MaxBackoff {
		backoff = c.cfg.MCP.Retry.MaxBackoff // Cap at 30s
	}
	select {
	case <-ctx.Done():
		slog.Debug("backoff cancelled")
	case <-time.After(backoff):
	}
}
