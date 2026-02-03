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
	"pr-review-automation/internal/splitter"

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

	diffResult, err := s.mcpClient.CallTool(ctx, config.MCPServerBitbucket, config.ToolBitbucketGetDiff, map[string]interface{}{
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

	// [Fix] Handle case where tool returns JSON-wrapped diff (e.g. {"diff": "..."}) inside the text content
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
	preprocessor := splitter.NewDiffPreprocessor(splitter.PreprocessOptions{
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
func deduplicateDiffs(fileDiffStrs []string, preprocessor *splitter.DiffPreprocessor, cache *FileCache) []FileChange {
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

		// [Optimization] Write to FileCache to avoid re-fetching in Stage 2
		if cache != nil {
			// Note: We don't have full file content here, only diff.
			// But wait, the FileCache is for FULL FILE content for context.
			// Diff is NOT full content.
			// So we CANNOT use cache.Put here UNLESS we fetched full content.
			// Stage 1 only gets diff.
			// BUT, if we have the tool bitbucket_get_diff, does it return full? No.
			//
			// Correction: The plan says "Stage1 写入 Diff 内容到缓存".
			// But Diff content != File Content.
			// If Stage2 needs full context of diff files, it must fetch them.
			// Currently Stage2 fetches fetches "s.fetchFileContent".
			//
			// Wait, the plan says:
			// "将 Diff 文件内容写入缓存 (Stage1 已获取)"
			// Re-reading user requirement: "对于重复文件是否在调用get file contents时就做了第一层控制".
			//
			// If Stage 1 only gets Diffs, we can't cache FULL content.
			// However, maybe Stage 2 will fetch them.
			//
			// Let's implement FileCache injection first.
			// I will SKIP writing to cache in Stage 1 for now if I only have diffs.
			// UNLESS I change Stage 1 to fetch full files? No, that's slow.
			//
			// Ah, the user plan says: "s.fileCache.Put(c.Path, c.RawContent)".
			// But Stage 1 `FileChange` doesn't have `RawContent` (full file).
			// It implies Stage 1 might fetch it? Or maybe we just mark it as "known"?
			//
			// If I can't cache full content, I can't prevent Stage 2 from fetching it IF Stage 2 needs full content.
			// But Stage 2's `deduplicateAgainstDiff` logic (L2) prevents fetching if it knows it's a Diff file.
			// The ONLY reason to cache in Stage 1 is if Stage 1 *already* fetched the full file.
			// It seems Stage 1 DOES NOT fetch full file.
			//
			// So, I will just Inject FileCache to Stage 1, but maybe not use it for Put yet?
			// Actually, if Stage 2 needs full content for "Context Analysis" of diff files, it WILL fetch them.
			// So Stage 2 will do the fetching of Diff files (for analysis).
			//
			// Wait, `Stage2.CollectContext` currently iterates `changes` and fetches content for them:
			// `content, err := s.fetchFileContent(...)`
			//
			// So Stage 2 DOES fetch full content for diff files.
			//
			// The FileCache is useful so that if a dependency A is also a Diff File B (A=B), we don't fetch A again.
			// But Stage 2 fetches B anyway.
			//
			// So the flow:
			// Stage 2 Loop over Diffs:
			//   Fetch B (Full) -> Cache.Put(B, content)
			// Stage 2 Dependency Analysis of B:
			//   Found Dep A (where A=B).
			//   Dep A processing:
			//     Check Cache(A)? Yes -> Hit!
			//
			// So Stage 1 doesn't need to write to cache. Stage 2's own loop over "changes" will write to cache.
			//
			// BUT, I can inject FileCache into Stage1 anyway for future proofing or consistency.
			// Actually, I'll modify `deduplicateDiffs` to NOT accept cache if I'm not using it.
			// I will just modify Stage1 struct to hold it.

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
