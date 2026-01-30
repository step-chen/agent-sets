package client

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"pr-review-automation/internal/types"
)

// refreshToolCache populates the tool cache from all connected MCP servers.
// It includes retry logic for robustness during startup.
func (c *MCPClient) refreshToolCache(ctx context.Context) error {
	maxRetries := 3
	var lastErr error

	for i := 0; i < maxRetries; i++ {
		err := c.doRefreshToolCache(ctx)
		if err == nil {
			slog.Info("refresh tool cache success", "attempt", i+1)
			return nil
		}
		lastErr = err
		slog.Warn("failed to refresh tool cache, retrying...", "attempt", i+1, "error", err)

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
	return fmt.Errorf("failed to refresh tool cache after %d attempts: %w", maxRetries, lastErr)
}

// doRefreshToolCache performs the actual tool fetching
func (c *MCPClient) doRefreshToolCache(ctx context.Context) error {
	// Snapshot endpoints to avoid holding lock during network calls
	c.mu.RLock()
	var serverNames []string
	for k := range c.endpoints {
		serverNames = append(serverNames, k)
	}
	c.mu.RUnlock()

	newCache := make(map[string][]types.RawToolSchema)
	var errs []error

	for _, name := range serverNames {
		// Reuse existing session
		session, err := c.getOrReconnect(name)
		if err != nil {
			slog.Error("tool cache: connect failed", "server", name, "error", err)
			errs = append(errs, fmt.Errorf("connect %s: %w", name, err))
			continue
		}

		// List tools using MCP SDK
		// ListTools(ctx context.Context, cursor *string) (*ListToolsResult, error)
		toolsResult, err := session.ListTools(ctx, nil)
		if err != nil {
			slog.Error("tool cache: ListTools failed", "server", name, "error", err)
			errs = append(errs, fmt.Errorf("list tools %s: %w", name, err))
			continue
		}

		var schemas []types.RawToolSchema
		for _, t := range toolsResult.Tools {
			schema := types.RawToolSchema{
				Name: t.Name,
			}

			// t.InputSchema is interface{} (JSON schema object)
			// Ensure it's map[string]interface{}
			if t.InputSchema != nil {
				if m, ok := t.InputSchema.(map[string]interface{}); ok {
					schema.InputSchema = m
				} else {
					// Try to marshal/unmarshal if it's some other type
					b, err := json.Marshal(t.InputSchema)
					if err == nil {
						var m map[string]interface{}
						if json.Unmarshal(b, &m) == nil {
							schema.InputSchema = m
						}
					}
				}
			}
			schemas = append(schemas, schema)
		}
		newCache[name] = schemas
		slog.Debug("tool cache: fetched", "server", name, "tools", len(schemas))
	}

	if len(errs) > 0 {
		return fmt.Errorf("errors fetching tools: %v", errs)
	}

	// Update cache thread-safely
	c.toolCacheMu.Lock()
	c.toolCache = newCache
	c.toolCacheMu.Unlock()
	return nil
}

// GetRawToolSchemas fetches raw tool schemas directly from MCP servers.
// Now it returns the cached data.
func (c *MCPClient) GetRawToolSchemas() map[string][]types.RawToolSchema {
	c.toolCacheMu.RLock()
	defer c.toolCacheMu.RUnlock()

	// Return a copy to avoid race conditions if caller modifies the map (though slice content is shared)
	result := make(map[string][]types.RawToolSchema)
	for k, v := range c.toolCache {
		result[k] = v
	}
	return result
}
