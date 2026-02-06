package pipeline

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
	"strconv"
	"strings"

	"pr-review-automation/internal/client"
	"pr-review-automation/internal/config"

	"github.com/tidwall/gjson"
)

// Stage1 implements the Diff Extraction stage
type Stage1 struct {
	cfg          *config.PipelineConfig
	mcpClient    *client.MCPClient
	llm          LLMClient
	promptLoader *PromptLoader
	fileCache    *FileCache
}

// NewStage1 creates a new Stage1 instance
func NewStage1(cfg *config.PipelineConfig, mcpClient *client.MCPClient, llm LLMClient, promptLoader *PromptLoader, cache *FileCache) *Stage1 {
	return &Stage1{
		cfg:          cfg,
		mcpClient:    mcpClient,
		llm:          llm,
		promptLoader: promptLoader,
		fileCache:    cache,
	}
}

// ExtractDiffs implements the Stage1DiffExtractor interface
func (s *Stage1) ExtractDiffs(ctx context.Context, req ReviewRequest) ([]FileChange, error) {
	slog.Info("Stage 1: Starting Diff Extraction", "pr_id", req.PR.ID)

	// 1. Execute Tool: Get Diff
	// We default to bitbucket_get_pull_request_diff as it is the primary tool.
	// In a future advanced version, we could use LLM to decide the tool,
	// but for "Diff Extraction" stage, it is deterministic enough.

	prID, err := strconv.Atoi(req.PR.ID)
	if err != nil {
		return nil, fmt.Errorf("invalid pull request ID: %w", err)
	}

	diffResult, err := s.mcpClient.CallToolByKey(ctx, config.MCPServerBitbucket, config.ToolKeyGetDiff, map[string]interface{}{
		"projectKey":    req.PR.ProjectKey,
		"repoSlug":      req.PR.RepoSlug,
		"pullRequestId": prID,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to get diff: %w", err)
	}

	// 2. Extract Diff String
	diffStr := ExtractString(diffResult, "content.0.text", "output.diff", "output.text", "output", "diff")
	if diffStr == "" {
		return nil, fmt.Errorf("empty diff content extracted")
	}

	// Handle case where tool returns JSON-wrapped diff (e.g. {"diff": "..."}) inside the text content
	if strings.Contains(diffStr, "\"diff\"") && strings.HasPrefix(strings.TrimSpace(diffStr), "{") {
		if gjson.Valid(diffStr) {
			val := gjson.Get(diffStr, "diff").String()
			if val != "" {
				slog.Debug("unwrapped json diff", "original_len", len(diffStr), "new_len", len(val))
				diffStr = val
			}
		}
	}

	if diffStr == "" {
		// Verify again after unwrapping
		return nil, fmt.Errorf("empty diff content after unwrapping")
	}

	// 3. Parse Diff into FileChanges
	preprocessor := NewDiffPreprocessor(PreprocessOptions{
		RemoveWhitespace: true,
		FoldDeletesOver:  10,
	})

	// Preprocess first to clean up noise
	cleanDiff := preprocessor.Preprocess(diffStr)

	// Split into per-file chunks
	fileDiffStrs := preprocessor.SplitByFile(cleanDiff)

	changes := deduplicateDiffs(fileDiffStrs, preprocessor, s.fileCache)

	slog.Info("Stage 1: Completed", "files_changed", len(changes))
	return changes, nil
}

// deduplicateDiffs removes duplicates based on diff content hash
// Keeps the first occurrence of a file path, skips subsequent identical content files
func deduplicateDiffs(fileDiffStrs []string, preprocessor *DiffPreprocessor, cache *FileCache) []FileChange {
	seen := make(map[string]string) // hash -> first path
	var changes []FileChange

	for _, fdStr := range fileDiffStrs {
		path := preprocessor.ExtractFilePath(fdStr)
		hunkLines := strings.Split(fdStr, "\n")

		// Calculate diff content hash (excluding path info)
		hash := computeDiffHash(hunkLines)

		if firstPath, exists := seen[hash]; exists {
			slog.Warn("Duplicate diff content detected, skipping",
				"skipped_path", path,
				"kept_path", firstPath,
				"hash", hash[:12])
			continue
		}

		seen[hash] = path

		// Write to FileCache to avoid re-fetching in Stage 2
		if cache != nil {

		}

		changes = append(changes, FileChange{
			Path:       path,
			ChangeType: "modify", // Simplified
			HunkLines:  hunkLines,
		})
	}

	return changes
}

// computeDiffHash calculates SHA256 hash of diff content

// Ignores diff header lines (--- +++ @@) to rely only on actual changes
func computeDiffHash(lines []string) string {
	h := sha256.New()
	for _, line := range lines {
		// Skip diff metadata lines
		if strings.HasPrefix(line, "---") ||
			strings.HasPrefix(line, "+++") ||
			strings.HasPrefix(line, "@@") ||
			strings.HasPrefix(line, "diff --git") ||
			strings.HasPrefix(line, "index ") {
			continue
		}
		h.Write([]byte(line))
		h.Write([]byte("\n"))
	}
	return hex.EncodeToString(h.Sum(nil))
}
