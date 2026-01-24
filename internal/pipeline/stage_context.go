package pipeline

import (
	"context"
	"log/slog"
	"sync"

	"pr-review-automation/internal/client"
	"pr-review-automation/internal/config"
	"pr-review-automation/internal/domain"
)

// Stage2 implements the Context Collection stage
type Stage2 struct {
	cfg          *config.PipelineConfig
	mcpClient    *client.MCPClient
	llm          LLMClient
	promptLoader *PromptLoader
}

// NewStage2 creates a new Stage2 instance
func NewStage2(cfg *config.PipelineConfig, mcpClient *client.MCPClient, llm LLMClient, promptLoader *PromptLoader) *Stage2 {
	return &Stage2{
		cfg:          cfg,
		mcpClient:    mcpClient,
		llm:          llm,
		promptLoader: promptLoader,
	}
}

// CollectContext implements the Stage2ContextCollector interface
func (s *Stage2) CollectContext(ctx context.Context, req ReviewRequest, changes []FileChange) ([]FileContent, error) {
	slog.Info("Stage 2: Starting Context Collection", "files_changed", len(changes))

	var collected []FileContent
	var mu sync.Mutex
	var wg sync.WaitGroup
	semaphore := make(chan struct{}, 5) // Concurrency limit

	for _, change := range changes {
		// Skip deleted files
		if change.ChangeType == "delete" {
			continue
		}

		wg.Add(1)
		go func(c FileChange) {
			defer wg.Done()
			semaphore <- struct{}{}
			defer func() { <-semaphore }()

			content, err := s.fetchFileContent(ctx, req.PR, c.Path, req.LatestCommit)
			if err != nil {
				slog.Warn("failed to fetch file content", "path", c.Path, "error", err)
				return
			}

			// Check file size limit
			if len(content) > s.cfg.Stage2Context.MaxFileSize {
				slog.Info("file too large, skipping content", "path", c.Path, "size", len(content))
				return
			}

			mu.Lock()
			collected = append(collected, FileContent{
				Path:      c.Path,
				Content:   content,
				IsDiffed:  true,
				Relevance: "direct",
			})
			mu.Unlock()
		}(change)
	}

	wg.Wait()

	// TODO: Future enhancement - Use LLM to identify and fetch related dependency files
	// based on the changes (e.g. if a test is modified, fetch the implementation).

	slog.Info("Stage 2: Completed", "files_collected", len(collected))
	return collected, nil
}

func (s *Stage2) fetchFileContent(ctx context.Context, pr domain.PullRequest, path string, commitID string) (string, error) {
	// Use bitbucket_get_content or similar MCP tool
	// Arguments per bitbucket MCP tool definition (usually requires repo, project, etc)

	// Note: We need to ensure we use the correct tool name and arguments.
	// Based on MCP server config, it's likely "bitbucket_get_file_content".

	// Arguments for bitbucket_get_file_content: projectKey, repoSlug, path, at (commit)

	result, err := s.mcpClient.CallTool(ctx, config.MCPServerBitbucket, "bitbucket_get_file_content", map[string]interface{}{
		"projectKey": pr.ProjectKey,
		"repoSlug":   pr.RepoSlug,
		"path":       path,
		"at":         commitID,
	})
	if err != nil {
		return "", err
	}

	return ExtractString(result, "content.0.text", "output.text", "output"), nil
}
