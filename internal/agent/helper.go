package agent

import (
	"context"
	"encoding/json"
	"log/slog"
	"strconv"

	"pr-review-automation/internal/config"
	"pr-review-automation/internal/domain"

	"github.com/tidwall/gjson"
)

// ExtractString extracts a string value from an any-typed result (map or struct)
// by trying multiple possible keys.
func ExtractString(data any, keys ...string) string {
	if s, ok := data.(string); ok {
		// If it's a string, it might be a JSON string we want to extract from
		if gjson.Valid(s) {
			for _, k := range keys {
				val := gjson.Get(s, k).String()
				if val != "" {
					return val
				}
			}
		}
		return s
	}

	// Marshal to JSON for query
	b, err := json.Marshal(data)
	if err != nil {
		return ""
	}

	jsonStr := string(b)
	shortJSON := jsonStr
	if len(shortJSON) > 200 {
		shortJSON = shortJSON[:200] + "..."
	}
	slog.Debug("ExtractString searching", "keys", keys, "json_snippet", shortJSON)

	for _, k := range keys {
		val := gjson.GetBytes(b, k).String()
		if val != "" {
			slog.Debug("ExtractString matched", "key", k)
			return val
		}
	}

	// If no keys matched, return the marshaled JSON if it's not empty/null
	if jsonStr == "{}" || jsonStr == "[]" || jsonStr == "null" {
		return ""
	}
	slog.Debug("ExtractString fallback to raw JSON")
	return jsonStr
}

// ToolInvoker defines the interface for invoking MCP tools
type ToolInvoker interface {
	CallTool(ctx context.Context, serverName, toolName string, args map[string]interface{}) (any, error)
}

// FetchChangedFiles retrieves the list of changed file paths from the PR.
// Returns empty slice on error (falls back to default language).
func FetchChangedFiles(ctx context.Context, invoker ToolInvoker, pr *domain.PullRequest) []string {
	// pr.ID is string, converting to int for MCP
	prID, _ := strconv.Atoi(pr.ID)
	result, err := invoker.CallTool(ctx, config.MCPServerBitbucket, config.ToolBitbucketGetChanges, map[string]interface{}{
		"projectKey":    pr.ProjectKey,
		"repoSlug":      pr.RepoSlug,
		"pullRequestId": prID,
	})
	if err != nil {
		slog.Debug("fetch changed files failed", "error", err)
		return nil
	}

	// Parse result to extract file paths
	// Result structure: { "values": [{ "path": { "name": "foo.go" } }, ...] }
	// Use gjson for safe extraction
	jsonStr, _ := json.Marshal(result)
	var files []string
	// Support both path.name and path.toString which are common in Bitbucket
	gjson.GetBytes(jsonStr, "values.#.path.name").ForEach(func(_, v gjson.Result) bool {
		files = append(files, v.String())
		return true
	})
	if len(files) == 0 {
		gjson.GetBytes(jsonStr, "values.#.path.toString").ForEach(func(_, v gjson.Result) bool {
			files = append(files, v.String())
			return true
		})
	}
	return files
}
