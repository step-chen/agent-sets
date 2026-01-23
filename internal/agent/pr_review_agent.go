package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"iter"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"

	"pr-review-automation/internal/aggregator"
	"pr-review-automation/internal/client"
	"pr-review-automation/internal/config"
	"pr-review-automation/internal/domain"
	"pr-review-automation/internal/metrics"
	"pr-review-automation/internal/splitter"
	"pr-review-automation/internal/types"

	"google.golang.org/adk/agent"
	"google.golang.org/adk/agent/llmagent"
	"google.golang.org/adk/model"
	"google.golang.org/adk/runner"
	"google.golang.org/adk/session"
	"google.golang.org/genai"
)

// PRReviewAgent represents a PR review agent powered by ADK-Go
type PRReviewAgent struct {
	llm            model.LLM
	sessionService session.Service
	mcpClient      *client.MCPClient
	promptLoader   *PromptLoader
	modelName      string
	cfg            config.AgentConfig
}

// ReviewResult represents the result of a PR review
type ReviewResult struct {
	Comments []ReviewComment `json:"comments"`
	Score    int             `json:"score"`
	Summary  string          `json:"summary"`
	Model    string          `json:"model,omitempty"`
}

// ReviewRequest represents a PR review request with deduplication context.
// HistoricalComments are fetched from Bitbucket and injected into the prompt
// to enable LLM-driven deduplication.
type ReviewRequest struct {
	PR                 *domain.PullRequest
	HistoricalComments []ReviewComment // Existing AI comments from Bitbucket
}

// ReviewComment represents a single comment in a PR review
type ReviewComment struct {
	File    string `json:"file"`
	Line    int    `json:"line"`
	Comment string `json:"comment"`
}

// NewPRReviewAgent creates a new PR review agent factory
func NewPRReviewAgent(llm model.LLM, mcpClient *client.MCPClient, promptLoader *PromptLoader, modelName string, agentCfg config.AgentConfig) (*PRReviewAgent, error) {
	// Validation of dependencies
	if llm == nil {
		return nil, fmt.Errorf("llm is nil")
	}
	if mcpClient == nil {
		return nil, fmt.Errorf("mcp client is nil")
	}
	if promptLoader == nil {
		return nil, fmt.Errorf("prompt loader is nil")
	}

	// Apply defaults
	if agentCfg.MaxIterations <= 0 {
		agentCfg.MaxIterations = 20
	}
	if agentCfg.MaxToolCalls <= 0 {
		agentCfg.MaxToolCalls = 50
	}

	return &PRReviewAgent{
		llm:            llm,
		sessionService: session.InMemoryService(),
		mcpClient:      mcpClient,
		promptLoader:   promptLoader,
		modelName:      modelName,
		cfg:            agentCfg,
	}, nil
}

// Name returns the name of the reviewer backend
func (pra *PRReviewAgent) Name() string {
	return "adk"
}

// ReviewPR orchestrates the PR review process with automatic fallback on token limit errors
func (pra *PRReviewAgent) ReviewPR(ctx context.Context, req *ReviewRequest) (*ReviewResult, error) {
	slog.Info("Starting PR review", "pr_id", req.PR.ID)

	var result *ReviewResult
	var err error

	// Check if direct mode is forced via config
	if pra.cfg.DirectMode {
		slog.Info("Direct mode forced via config", "pr_id", req.PR.ID)
		return pra.reviewPRDirect(ctx, req)
	}

	// Check if chunked review is enabled
	if pra.cfg.ChunkReview.Enabled {
		result, err = pra.reviewPRChunked(ctx, req)
	} else {
		result, err = pra.reviewPRStandard(ctx, req)
	}

	// Fallback to Direct Completion mode on token limit or critical errors
	if isTokenLimitError(err) {
		slog.Warn("Token limit exceeded, falling back to Direct mode", "error", err)
		return pra.reviewPRDirect(ctx, req)
	}

	return result, err
}

// isTokenLimitError checks if the error is due to token/context limit
func isTokenLimitError(err error) bool {
	if err == nil {
		return false
	}
	errStr := strings.ToLower(err.Error())
	return strings.Contains(errStr, "context_length_exceeded") ||
		strings.Contains(errStr, "maximum context length") ||
		strings.Contains(errStr, "context window") ||
		strings.Contains(errStr, "token limit") ||
		strings.Contains(errStr, "too many tokens")
}

// reviewPRStandard performs the standard single-pass review
func (pra *PRReviewAgent) reviewPRStandard(ctx context.Context, req *ReviewRequest) (*ReviewResult, error) {
	pr := req.PR
	start := time.Now()
	metricResult := "error"
	defer func() {
		metrics.ProcessingDuration.WithLabelValues(metricResult).Observe(time.Since(start).Seconds())
	}()

	// 1. Get FRESH Toolsets
	toolsets := pra.mcpClient.GetToolsets()
	if len(toolsets) == 0 {
		slog.Warn("no mcp toolsets available")
	}

	// 2. Load prompt (project from PR, language detection)
	language := "default"
	if changedFiles := FetchChangedFiles(ctx, pra.mcpClient, pr); len(changedFiles) > 0 {
		language = DetectLanguage(changedFiles)
		slog.Debug("detected language", "language", language, "files", len(changedFiles))
	}
	instruction, err := pra.promptLoader.Load(pr.ProjectKey, language, map[string]interface{}{
		"ProjectKey": pr.ProjectKey,
		"RepoSlug":   pr.RepoSlug,
	})
	if err != nil {
		return nil, fmt.Errorf("load prompt: %w", err)
	}

	// 3. Create Ephemeral Agent Configuration
	adkAgent, err := llmagent.New(
		llmagent.Config{
			Name:        "pr-review-agent-" + pr.ID,
			Description: "Ephemeral PR review agent",
			Model:       &jsonLLM{LLM: pra.llm},
			Instruction: instruction,
			Toolsets:    toolsets,
		},
	)
	if err != nil {
		return nil, fmt.Errorf("create agent: %w", err)
	}

	// Convert PR info to a prompt
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf(`Review PR #%v in %v/%v

Title: %v
Desc: %v
Author: %v
LatestCommit: %v
`,
		pr.ID,
		pr.ProjectKey,
		pr.RepoSlug,
		pr.Title,
		pr.Description,
		pr.Author,
		pr.LatestCommit,
	))

	// Inject historical comments for deduplication
	if len(req.HistoricalComments) > 0 {
		sb.WriteString("## Known Issues (DO NOT DUPLICATE)\n")
		for _, c := range req.HistoricalComments {
			truncatedComment := c.Comment
			if len(truncatedComment) > 50 {
				truncatedComment = truncatedComment[:50] + "..."
			}
			sb.WriteString(fmt.Sprintf("- %s:%d %s\n", c.File, c.Line, truncatedComment))
		}
		sb.WriteString("\n")
	}

	sb.WriteString("Fetch diff and review. Check related Jira if ticket ID in title. Output JSON only.")

	result, err := pra.executeAgent(ctx, adkAgent, sb.String(), pr)
	if err == nil {
		metricResult = "success"
		metrics.PullRequestTotal.WithLabelValues("success").Inc()
	}
	return result, err
}

// reviewPRChunked performs review by splitting diff into chunks
func (pra *PRReviewAgent) reviewPRChunked(ctx context.Context, req *ReviewRequest) (*ReviewResult, error) {
	pr := req.PR
	start := time.Now()
	metricResult := "error"
	defer func() {
		metrics.ProcessingDuration.WithLabelValues(metricResult).Observe(time.Since(start).Seconds())
	}()

	// 1. Fetch full diff manually (bypass Agent tool loop for this step)
	prID, _ := strconv.Atoi(pr.ID)
	args := map[string]interface{}{
		"projectKey":    pr.ProjectKey,
		"repoSlug":      pr.RepoSlug,
		"pullRequestId": prID,
	}
	diffResult, err := pra.mcpClient.CallTool(ctx, config.MCPServerBitbucket, config.ToolBitbucketGetDiff, args)
	if err != nil {
		slog.Error("failed to fetch diff for splitting", "error", err)
		return pra.reviewPRStandard(ctx, req)
	}
	slog.Info("Fetched diff for splitting", "type", fmt.Sprintf("%T", diffResult))

	// Handle different result types from CallTool via robust helper
	diffStr := ExtractString(diffResult, "content.0.text", "output.diff", "output.text", "output", "diff")

	if diffStr == "" {
		slog.Warn("diff result is empty after extraction", "type", fmt.Sprintf("%T", diffResult))
		return pra.reviewPRStandard(ctx, req)
	}
	slog.Info("Diff extracted successfully", "len", len(diffStr), "preview", diffStr[:min(len(diffStr), 100)])

	// 2. Preprocess diff to reduce token usage
	preprocessOpts := splitter.DefaultPreprocessOptions()
	preprocessOpts.RemoveWhitespace = pra.cfg.ChunkReview.RemoveWhitespace
	preprocessOpts.RemoveBinaryDiff = pra.cfg.ChunkReview.RemoveBinaryDiff
	preprocessOpts.FoldDeletesOver = pra.cfg.ChunkReview.FoldDeletesOver
	preprocessOpts.MaxContextLines = pra.cfg.ChunkReview.ContextLines
	preprocessOpts.CompressSpaces = pra.cfg.ChunkReview.CompressSpaces
	preprocessor := splitter.NewDiffPreprocessor(preprocessOpts)
	originalLen := len(diffStr)
	diffStr = preprocessor.Preprocess(diffStr)
	slog.Info("Diff preprocessed", "original_bytes", originalLen, "processed_bytes", len(diffStr))

	// 3. Split diff with context preservation
	contextLines := pra.cfg.ChunkReview.ContextLines
	if contextLines <= 0 {
		contextLines = 20 // Default
	}
	sp := splitter.NewDiffSplitterWithContext(
		pra.cfg.ChunkReview.MaxTokensPerChunk,
		pra.cfg.ChunkReview.MaxFilesPerChunk,
		contextLines,
	)
	chunks := sp.Split(diffStr)
	slog.Info("Split result", "chunks", len(chunks), "diff_bytes", len(diffStr), "context_lines", contextLines)

	if len(chunks) == 0 {
		slog.Warn("no diff chunks generated, falling back to standard review")
		return pra.reviewPRStandard(ctx, req)
	}

	slog.Info("Splitting PR into chunks", "count", len(chunks))

	// 3. Parallel Execution
	results := make([]aggregator.ChunkReviewResult, len(chunks))
	var wg sync.WaitGroup
	semaphore := make(chan struct{}, pra.cfg.ChunkReview.ParallelChunks)

	for i, chunk := range chunks {
		wg.Add(1)
		go func(idx int, c splitter.DiffChunk) {
			defer wg.Done()
			semaphore <- struct{}{}        // Acquire
			defer func() { <-semaphore }() // Release

			res, err := pra.reviewSingleChunk(ctx, req, c)
			results[idx] = aggregator.ChunkReviewResult{
				ChunkID:     c.ChunkID,
				TotalChunks: c.TotalChunks,
				Error:       err,
			}
			if res != nil {
				// Convert to aggregator.ReviewComment
				aggComments := make([]aggregator.ReviewComment, len(res.Comments))
				for k, rc := range res.Comments {
					aggComments[k] = aggregator.ReviewComment{
						File:    rc.File,
						Line:    rc.Line,
						Comment: rc.Comment,
					}
				}
				results[idx].Comments = aggComments
				results[idx].Score = res.Score
				results[idx].Summary = res.Summary
			}
		}(i, chunk)
	}

	wg.Wait()

	// 4. Aggregate results
	agg := aggregator.NewResultAggregator()
	finalRes := agg.Aggregate(results)

	metricResult = "success"
	metrics.PullRequestTotal.WithLabelValues("success").Inc()

	// Convert back to ReviewComment
	finalComments := make([]ReviewComment, len(finalRes.Comments))
	for k, rc := range finalRes.Comments {
		finalComments[k] = ReviewComment{
			File:    rc.File,
			Line:    rc.Line,
			Comment: rc.Comment,
		}
	}

	return &ReviewResult{
		Comments: finalComments,
		Score:    finalRes.Score,
		Summary:  finalRes.Summary,
		Model:    pra.modelName,
	}, nil
}

// reviewSingleChunk reviews a specific chunk of the diff
func (pra *PRReviewAgent) reviewSingleChunk(ctx context.Context, req *ReviewRequest, chunk splitter.DiffChunk) (*ReviewResult, error) {
	pr := req.PR

	// Use minimal toolset for chunked review to prevent aimless exploration.
	// Diff is already provided; only allow get_file_content for additional context.
	toolsets := pra.mcpClient.GetFilteredToolsets(config.ChunkedReviewAllowedTools)

	// Load prompt for this chunk
	// We use the same language detection logic but scoped to the chunk files
	chunkFilePaths := chunk.FileList()
	language := DetectLanguage(chunkFilePaths)
	instruction, err := pra.promptLoader.Load(pr.ProjectKey, language, map[string]interface{}{
		"ProjectKey": pr.ProjectKey,
		"RepoSlug":   pr.RepoSlug,
	})
	if err != nil {
		return nil, fmt.Errorf("load prompt: %w", err)
	}

	adkAgent, err := llmagent.New(
		llmagent.Config{
			Name:        fmt.Sprintf("pr-review-agent-%s-chunk-%d", pr.ID, chunk.ChunkID),
			Description: "Ephemeral PR chunk review agent",
			Model:       &jsonLLM{LLM: pra.llm},
			Instruction: instruction,
			Toolsets:    toolsets,
		},
	)
	if err != nil {
		return nil, fmt.Errorf("create chunk agent: %w", err)
	}

	// Construct Chunk Specific Prompt
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Review PR #%v (Chunk %d/%d) in %v/%v\n",
		pr.ID, chunk.ChunkID, chunk.TotalChunks, pr.ProjectKey, pr.RepoSlug))
	sb.WriteString(fmt.Sprintf("Title: %s\nAuthor: %s\nDesc: %s\nLatestCommit: %s\n\n",
		pr.Title, pr.Author, pr.Description, pr.LatestCommit))

	sb.WriteString("## Files in this chunk:\n")
	for _, f := range chunk.FileList() {
		sb.WriteString(fmt.Sprintf("- %s\n", f))
	}
	sb.WriteString("\n")

	sb.WriteString("## Diff Content (Do NOT fetch diff again, use this):\n")
	sb.WriteString("```diff\n")
	sb.WriteString(chunk.CombineContent())
	sb.WriteString("\n```\n\n")

	// Filter historical comments for this chunk
	chunkFiles := make(map[string]bool)
	for _, f := range chunk.Files {
		chunkFiles[f.Path] = true
	}

	if len(req.HistoricalComments) > 0 {
		hasRelevantComments := false
		var commentsSb strings.Builder
		commentsSb.WriteString("## Known Issues in these files (DO NOT DUPLICATE)\n")
		for _, c := range req.HistoricalComments {
			if chunkFiles[c.File] {
				hasRelevantComments = true
				truncated := c.Comment
				if len(truncated) > 50 {
					truncated = truncated[:50] + "..."
				}
				commentsSb.WriteString(fmt.Sprintf("- %s:%d %s\n", c.File, c.Line, truncated))
			}
		}
		if hasRelevantComments {
			sb.WriteString(commentsSb.String())
			sb.WriteString("\n")
		}
	}

	sb.WriteString("Review the code changes above. Check related Jira if ticket ID in title. Output JSON only.")

	return pra.executeAgent(ctx, adkAgent, sb.String(), pr)
}

// executeAgent runs the agent loop for a given prompt
func (pra *PRReviewAgent) executeAgent(ctx context.Context, adkAgent agent.Agent, prompt string, pr *domain.PullRequest) (*ReviewResult, error) {
	// Unique session ID per run to avoid conflicts in parallel execution
	sessionID := fmt.Sprintf("review-%v-%v-%d-%d", pr.RepoSlug, pr.ID, time.Now().UnixNano(), time.Now().Nanosecond())

	r, err := runner.New(runner.Config{
		AppName:        "pr-review",
		Agent:          adkAgent,
		SessionService: pra.sessionService,
	})
	if err != nil {
		return nil, fmt.Errorf("create runner: %w", err)
	}

	// Ensure session is created
	_, err = pra.sessionService.Create(ctx, &session.CreateRequest{
		AppName:   "pr-review",
		UserID:    "automation-user",
		SessionID: sessionID,
	})
	if err != nil && !strings.Contains(err.Error(), "already exists") {
		slog.Debug("create session failed", "session_id", sessionID, "error", err)
	}

	// CLEANUP: Delete session after execution
	defer func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		pra.sessionService.Delete(cleanupCtx, &session.DeleteRequest{
			AppName:   "pr-review",
			UserID:    "automation-user",
			SessionID: sessionID,
		})
	}()

	msg := &genai.Content{
		Parts: []*genai.Part{
			{Text: prompt},
		},
		Role: "user",
	}

	var finalText string
	eventCount := 0

	slog.Debug("running agent", "session", sessionID)

	// Run the agent loop
	for event, err := range r.Run(ctx, "automation-user", sessionID, msg, agent.RunConfig{}) {
		eventCount++
		if eventCount > pra.cfg.MaxIterations {
			return nil, fmt.Errorf("agent iteration limit exceeded (%d)", pra.cfg.MaxIterations)
		}

		if err != nil {
			return nil, fmt.Errorf("agent exec: %w", err)
		}

		if event.IsFinalResponse() {
			slog.Debug("agent event", "count", eventCount, "final", true)
			for _, part := range event.LLMResponse.Content.Parts {
				finalText += part.Text
			}
		}
	}

	if finalText == "" {
		return nil, fmt.Errorf("no response content")
	}

	// Clean up markdown code blocks
	finalText = types.CleanJSONFromMarkdown(finalText)

	var result ReviewResult
	if err := json.Unmarshal([]byte(finalText), &result); err != nil {
		// Fallback: simple JSON extraction
		start := strings.Index(finalText, "{")
		end := strings.LastIndex(finalText, "}")
		if start != -1 && end != -1 && end > start {
			if err2 := json.Unmarshal([]byte(finalText[start:end+1]), &result); err2 == nil {
				result.Model = pra.modelName
				return &result, nil
			}
		}
		slog.Error("failed to parse agent response", "text", finalText, "error", err)
		return nil, fmt.Errorf("invalid json response from agent: %w", err)
	}

	result.Model = pra.modelName
	return &result, nil
}

// fetchChangedFiles retrieves the list of changed file paths from the PR.
// Returns empty slice on error (falls back to default language).

// jsonLLM is a wrapper around model.LLM that invalidates JSON mode
type jsonLLM struct {
	model.LLM
}

func (j *jsonLLM) GenerateContent(ctx context.Context, req *model.LLMRequest, streaming bool) iter.Seq2[*model.LLMResponse, error] {
	if req.Config == nil {
		req.Config = &genai.GenerateContentConfig{}
	}
	req.Config.ResponseMIMEType = "application/json"
	return j.LLM.GenerateContent(ctx, req, streaming)
}

// reviewPRDirect performs a direct LLM completion without Agent loop
// This is used as fallback when token limits are exceeded
func (pra *PRReviewAgent) reviewPRDirect(ctx context.Context, req *ReviewRequest) (*ReviewResult, error) {
	pr := req.PR
	slog.Info("Starting Direct mode review", "pr_id", pr.ID)

	start := time.Now()
	metricResult := "error"
	defer func() {
		metrics.ProcessingDuration.WithLabelValues(metricResult + "_direct").Observe(time.Since(start).Seconds())
	}()

	// 1. Fetch diff directly (no Agent)
	prID, _ := strconv.Atoi(pr.ID)
	diffResult, err := pra.mcpClient.CallTool(ctx, config.MCPServerBitbucket, config.ToolBitbucketGetDiff, map[string]interface{}{
		"projectKey":    pr.ProjectKey,
		"repoSlug":      pr.RepoSlug,
		"pullRequestId": prID,
	})
	if err != nil {
		return nil, fmt.Errorf("fetch diff in direct mode: %w", err)
	}

	// Extract diff string
	// Extract diff string using robust helper
	diffStr := ExtractString(diffResult, "content.0.text", "output.diff", "output.text", "output", "diff")

	if diffStr == "" {
		return nil, fmt.Errorf("empty diff in direct mode")
	}

	// 2. Aggressive preprocessing for direct mode
	preprocessor := splitter.NewDiffPreprocessor(splitter.PreprocessOptions{
		MaxContextLines:  pra.cfg.ChunkReview.ContextLines,
		FoldDeletesOver:  pra.cfg.ChunkReview.FoldDeletesOver,
		RemoveBinaryDiff: pra.cfg.ChunkReview.RemoveBinaryDiff,
		RemoveWhitespace: pra.cfg.ChunkReview.RemoveWhitespace,
		CompressSpaces:   pra.cfg.ChunkReview.CompressSpaces,
	})
	diffStr = preprocessor.Preprocess(diffStr)

	// 3. Truncate if still too large
	maxChars := pra.cfg.MaxDirectChars
	if maxChars == 0 {
		maxChars = 40000
	}
	if len(diffStr) > maxChars {
		diffStr = diffStr[:maxChars] + "\n\n[... TRUNCATED FOR TOKEN LIMIT ...]"
	}

	// 4. Load prompt
	language := "default"
	if changedFiles := FetchChangedFiles(ctx, pra.mcpClient, pr); len(changedFiles) > 0 {
		language = DetectLanguage(changedFiles)
	}
	instruction, err := pra.promptLoader.Load(pr.ProjectKey, language, map[string]interface{}{
		"ProjectKey": pr.ProjectKey,
		"RepoSlug":   pr.RepoSlug,
	})
	if err != nil {
		instruction = "You are a code reviewer. Review the PR diff and output JSON with comments, score, and summary."
	}

	// 5. Build direct prompt
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Review PR #%s in %s/%s (DIRECT MODE - LIMITED CONTEXT)\n\n",
		pr.ID, pr.ProjectKey, pr.RepoSlug))
	sb.WriteString(fmt.Sprintf("Title: %s\nAuthor: %s\n\n", pr.Title, pr.Author))
	sb.WriteString("## Diff:\n```diff\n")
	sb.WriteString(diffStr)
	sb.WriteString("\n```\n\n")
	sb.WriteString("Review the code and output ONLY valid JSON:\n")
	sb.WriteString(`{"comments":[{"file":"path","line":123,"comment":"issue"}],"score":85,"summary":"verdict"}`)

	// 6. Direct LLM call using SimpleTextQuery
	llmAdapter, ok := pra.llm.(*client.OpenAIAdapter)
	if !ok {
		// If not OpenAIAdapter, try unwrapping jsonLLM
		if jsonWrapped, ok := pra.llm.(*jsonLLM); ok {
			if adapter, ok := jsonWrapped.LLM.(*client.OpenAIAdapter); ok {
				llmAdapter = adapter
			}
		}
	}

	if llmAdapter == nil {
		return nil, fmt.Errorf("direct mode requires OpenAIAdapter")
	}

	response, err := llmAdapter.SimpleTextQuery(ctx, instruction, sb.String())
	if err != nil {
		return nil, fmt.Errorf("direct LLM call: %w", err)
	}

	// 7. Parse response
	response = types.CleanJSONFromMarkdown(response)
	var result ReviewResult
	if err := json.Unmarshal([]byte(response), &result); err != nil {
		// Fallback extraction
		start := strings.Index(response, "{")
		end := strings.LastIndex(response, "}")
		if start != -1 && end != -1 && end > start {
			if err2 := json.Unmarshal([]byte(response[start:end+1]), &result); err2 == nil {
				result.Model = pra.modelName + " (direct)"
				metricResult = "success"
				return &result, nil
			}
		}
		return nil, fmt.Errorf("parse direct response: %w", err)
	}

	result.Model = pra.modelName + " (direct)"
	metricResult = "success"
	metrics.PullRequestTotal.WithLabelValues("success_direct").Inc()

	slog.Info("Direct mode review completed",
		"pr_id", pr.ID,
		"comments", len(result.Comments),
		"duration", time.Since(start))

	return &result, nil
}
