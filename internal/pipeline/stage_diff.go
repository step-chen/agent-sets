package pipeline

import (
	"context"
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
}

// NewStage1 creates a new Stage1 instance
func NewStage1(cfg *config.PipelineConfig, mcpClient *client.MCPClient, llm LLMClient, promptLoader *PromptLoader) *Stage1 {
	return &Stage1{
		cfg:          cfg,
		mcpClient:    mcpClient,
		llm:          llm,
		promptLoader: promptLoader,
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

	var changes []FileChange
	for _, fdStr := range fileDiffStrs {
		path := preprocessor.ExtractFilePath(fdStr)
		changes = append(changes, FileChange{
			Path:       path,
			ChangeType: "modify", // Simplified, logic to detect add/delete/rename can be added if needed
			HunkLines:  strings.Split(fdStr, "\n"),
		})
	}

	slog.Info("Stage 1: Completed", "files_changed", len(changes))
	return changes, nil
}
