package pipeline

import (
	"context"
	"log/slog"
	"sync"

	"pr-review-automation/internal/client"
	"pr-review-automation/internal/config"
	codecontext "pr-review-automation/internal/context"
	"pr-review-automation/internal/domain"
)

// Stage2 implements the Context Collection stage
type Stage2 struct {
	cfg           *config.PipelineConfig
	mcpClient     *client.MCPClient
	llm           LLMClient
	promptLoader  *PromptLoader
	contextEngine *codecontext.ContextEngine
	fileCache     *FileCache
}

// NewStage2 creates a new Stage2 instance
func NewStage2(cfg *config.PipelineConfig, mcpClient *client.MCPClient, llm LLMClient, promptLoader *PromptLoader, ctxEngine *codecontext.ContextEngine, fileCache *FileCache) *Stage2 {
	return &Stage2{
		cfg:           cfg,
		mcpClient:     mcpClient,
		llm:           llm,
		promptLoader:  promptLoader,
		contextEngine: ctxEngine,
		fileCache:     fileCache,
	}
}

// CollectContext implements the Stage2ContextCollector interface
func (s *Stage2) CollectContext(ctx context.Context, req ReviewRequest, changes []FileChange) ([]FileContent, error) {
	slog.Info("Stage 2: Starting Context Collection", "files_changed", len(changes))

	var collected []FileContent
	knownPaths := make(map[string]bool) // L1: Deduplication Set

	// 1. Process Diff Files (Base Context)
	// We must fetch full content for diff files to perform analysis (and eventual compression)
	var diffFiles []FileContent
	var depsCandidates []string

	for _, change := range changes {
		if change.ChangeType == "delete" {
			continue
		}

		path := change.Path
		knownPaths[path] = true // Mark as known

		// L3: Fetch using Cache (or Network)
		content, err := s.fileCache.GetOrFetch(ctx, path, func() (string, error) {
			return s.fetchFileContent(ctx, req.PR, path, req.LatestCommit)
		})

		if err != nil {
			slog.Warn("Failed to fetch diff file content", "path", path, "error", err)
			continue
		}

		// Check size limit (per file)
		if len(content) > s.cfg.Stage2Context.MaxFileSize {
			slog.Info("Diff file too large for context analysis", "path", path, "size", len(content))
			// We still keep it as a diff file, but maybe skip analysis?
			// For now, consistent with legacy behavior: skip content if too big?
			// Actually, if it's too big, we probably shouldn't pass it to Context Engine.
			// But we DO want the Diff to be reviewed.
			// However, `collected` is "Context Files".
			// The `Review` stage takes `changes` (Diffs) AND `contextFiles`.
			// `FileContent` here represents *additional context* or *full content of diffs*.
			// If we return it here, it might be used for "ContextReducer".
			// Let's stick to: Analyze valid files.
		} else {
			// Analyze
			analysis, err := s.contextEngine.Analyze(ctx, path, []byte(content))
			if err == nil {
				// Collect valid dependencies
				for _, d := range analysis.Dependencies {
					depsCandidates = append(depsCandidates, d.Path)
				}

				diffFiles = append(diffFiles, FileContent{
					Path:      path,
					Content:   content,
					IsDiffed:  true,
					Relevance: "direct",
					Analysis:  analysis,
				})
			} else {
				slog.Warn("Context analysis failed", "path", path, "error", err)
			}
		}
	}

	// Add diff files to result
	collected = append(collected, diffFiles...)

	// 2. Process Dependencies (Extra Context)
	// L2: Exclude files already in diff (checked via knownPaths)
	uniqueDeps := make([]string, 0)
	for _, depPath := range depsCandidates {
		// Clean path just in case
		// Note: depPath from tree-sitter might be relative or raw.
		// For now assume raw paths or minimal normalization if needed.
		// A real implementation would resolve paths relative to the source.
		// We'll trust exact match for now.

		if _, exists := knownPaths[depPath]; !exists {
			knownPaths[depPath] = true
			uniqueDeps = append(uniqueDeps, depPath)
		}
	}

	slog.Info("Stage 2: Dependency Candidates found", "count", len(uniqueDeps))

	// Limit number of extra files BEFORE fetching
	limit := s.cfg.Stage2Context.MaxExtraFiles
	if len(uniqueDeps) > limit {
		slog.Info("Stage 2: Truncating dependencies", "total", len(uniqueDeps), "limit", limit)
		uniqueDeps = uniqueDeps[:limit]
	}

	// Fetch Dependencies (concurrently)
	var depFiles []FileContent
	var mu sync.Mutex
	var wg sync.WaitGroup
	semaphore := make(chan struct{}, 5)

	for _, depPath := range uniqueDeps {
		wg.Add(1)
		go func(path string) {
			defer wg.Done()
			semaphore <- struct{}{}
			defer func() { <-semaphore }()

			content, err := s.fileCache.GetOrFetch(ctx, path, func() (string, error) {
				return s.fetchFileContent(ctx, req.PR, path, req.LatestCommit)
			})

			if err != nil {
				slog.Warn("Failed to fetch dependency", "path", path, "error", err)
				return
			}

			if len(content) > s.cfg.Stage2Context.MaxFileSize {
				slog.Debug("Dependency too large", "path", path)
				return
			}

			// Optional: analyze dependency too? For now, just add it.
			// Getting analysis for dependency helps later stages but might be expensive.
			// Let's analyze to get chunks (useful for reducer).
			analysis, err := s.contextEngine.Analyze(ctx, path, []byte(content))
			if err != nil {
				slog.Warn("Dependency analysis failed", "path", path)
			}

			mu.Lock()
			depFiles = append(depFiles, FileContent{
				Path:      path,
				Content:   content,
				IsDiffed:  false,
				Relevance: "dependency",
				Analysis:  analysis,
			})
			mu.Unlock()
		}(depPath)
	}
	wg.Wait()

	// 3. Global Size Limit Check (MaxTotalSizeKB)
	// We prioritize Diff files (already added), then Dependencies.
	// We might need to trim `depFiles` if we exceed total size.
	// But `collected` already has diffFiles.

	// Merge and check size
	// We can implement a helper or just do it here.
	// For simplicity, just append and let ContextReducer handle TOKEN limits.
	// But `MaxTotalSizeKB` is a "sanity check" to avoid passing 100MB to reducer.

	totalSize := 0
	for _, f := range collected {
		totalSize += len(f.Content)
	}

	limitBytes := s.cfg.Stage2Context.MaxTotalSizeKB * 1024
	acceptedDeps := 0

	for _, f := range depFiles {
		if totalSize+len(f.Content) > limitBytes {
			slog.Info("Stage 2: MaxTotalSizeKB reached, dropping remaining dependencies",
				"current_size", totalSize, "limit", limitBytes)
			break
		}
		collected = append(collected, f)
		totalSize += len(f.Content)
		acceptedDeps++
	}

	slog.Info("Stage 2: Completed",
		"diff_files", len(diffFiles),
		"deps_collected", acceptedDeps,
		"total_files", len(collected))

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
