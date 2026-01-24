package client

import (
	"encoding/json"
	"log/slog"

	"pr-review-automation/internal/types"
)

// GetRawToolSchemas fetches raw tool schemas directly from MCP servers.
func (c *MCPClient) GetRawToolSchemas() map[string][]types.RawToolSchema {
	result := make(map[string][]types.RawToolSchema)

	// Snapshot endpoints
	c.mu.RLock()
	var serverNames []string
	for k := range c.endpoints {
		serverNames = append(serverNames, k)
	}
	c.mu.RUnlock()

	for _, name := range serverNames {
		// Reuse existing session
		session, err := c.getOrReconnect(name)
		if err != nil {
			slog.Debug("GetRawToolSchemas: connect failed", "server", name, "error", err)
			continue
		}

		// List tools using MCP SDK
		// ListTools(ctx context.Context, cursor *string) (*ListToolsResult, error)
		toolsResult, err := session.ListTools(c.baseCtx, nil)
		if err != nil {
			slog.Debug("GetRawToolSchemas: ListTools failed", "server", name, "error", err)
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
		result[name] = schemas
		slog.Debug("GetRawToolSchemas: fetched", "server", name, "tools", len(schemas))
	}

	return result
}
