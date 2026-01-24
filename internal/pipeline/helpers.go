package pipeline

import (
	"context"
	"encoding/json"
	"log/slog"
	"path/filepath"
	"pr-review-automation/internal/config"
	"pr-review-automation/internal/domain"
	"strconv"
	"strings"

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

	// Marshaling to JSON for query
	b, err := json.Marshal(data)
	if err != nil {
		return ""
	}

	jsonStr := string(b)
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
	return jsonStr
}

// ToolInvoker defines the interface for invoking MCP tools
type ToolInvoker interface {
	CallTool(ctx context.Context, serverName, toolName string, args map[string]interface{}) (any, error)
}

// FetchChangedFiles retrieves the list of changed file paths from the PR.
// Returns empty slice on error (falls back to default language).
func FetchChangedFiles(ctx context.Context, invoker ToolInvoker, pr domain.PullRequest) []string {
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

// languageExtensions maps file extensions to language identifiers
var languageExtensions = map[string]string{
	".cpp": "cpp", ".cc": "cpp", ".cxx": "cpp", ".c": "cpp", ".h": "cpp", ".hpp": "cpp", ".hxx": "cpp",
	".go":   "golang",
	".py":   "python",
	".java": "java",
	".ts":   "typescript", ".tsx": "typescript",
	".js": "javascript", ".jsx": "javascript",
	".rs": "rust",
	".kt": "kotlin", ".kts": "kotlin",
	".swift": "swift",
	".rb":    "ruby",
	".cs":    "csharp",
}

// DetectLanguage detects the primary language from a list of file paths
// based on file extensions. Returns "default" if no language is detected.
func DetectLanguage(files []string) string {
	counts := make(map[string]int)

	for _, file := range files {
		ext := strings.ToLower(filepath.Ext(file))
		if lang, ok := languageExtensions[ext]; ok {
			counts[lang]++
		}
	}

	if len(counts) == 0 {
		return "default"
	}

	// Find the language with the most files
	maxLang := "default"
	maxCount := 0
	for lang, count := range counts {
		if count > maxCount {
			maxCount = count
			maxLang = lang
		}
	}

	return maxLang
}
