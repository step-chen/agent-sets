package client

import (
	"context"
	"fmt"
	"log/slog"

	"pr-review-automation/internal/metrics"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// CallTool calls a tool on a specific MCP server with retry logic
func (c *MCPClient) CallTool(ctx context.Context, serverName, toolName string, args map[string]interface{}) (any, error) {
	slog.Debug("call tool", "server", serverName, "tool", toolName)

	maxAttempts := 2
	var lastErr error

	for attempt := 0; attempt < maxAttempts; attempt++ {
		session, err := c.getOrReconnect(serverName)
		if err != nil {
			lastErr = err
			if attempt < maxAttempts-1 {
				c.forceReconnect(serverName)
				continue
			}
			return nil, err
		}

		// Execute Tool Call
		params := mcp.CallToolParams{
			Name:      toolName,
			Arguments: args,
		}

		result, err := session.CallTool(ctx, &params)
		if err == nil {
			metrics.MCPToolCalls.WithLabelValues(serverName, toolName, "success").Inc()

			// Check response filter
			c.mu.RLock()
			filter := c.responseFilters[serverName]
			c.mu.RUnlock()

			// Result is *mcp.CallToolResult.
			// Currently returning it directly or filtering it.
			// Pipeline expects a result that can be parsed (map or struct).
			// mcp.CallToolResult struct: { Content: []Content, IsError: bool }
			// We should probably return the result as is, or process content.
			// For backward compatibility (ADK tool returned map), we might need to verify what callers expect.
			// Stage1 uses ExtractString on result.

			if filter != nil {
				slog.Info("applying response filter", "server", serverName, "tool", toolName)
				// Filter expects 'any'.
				filtered := filter.Filter(toolName, result)
				return filtered, nil
			}

			return result, nil
		}

		lastErr = err
		slog.Warn("call tool failed", "server", serverName, "tool", toolName, "attempt", attempt, "error", err)

		if attempt < maxAttempts-1 {
			c.forceReconnect(serverName)
			c.backoff(ctx, attempt)
			continue
		}
	}

	metrics.MCPToolCalls.WithLabelValues(serverName, toolName, "error").Inc()
	return nil, fmt.Errorf("call tool %s/%s failed: %w", serverName, toolName, lastErr)
}
