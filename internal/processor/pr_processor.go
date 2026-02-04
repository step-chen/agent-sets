package processor

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"golang.org/x/sync/errgroup"

	"pr-review-automation/internal/client"
	"pr-review-automation/internal/config"
	"pr-review-automation/internal/domain"
	"pr-review-automation/internal/metrics"
	"pr-review-automation/internal/pipeline"
	"pr-review-automation/internal/storage"
	"pr-review-automation/internal/syncutil"
	"pr-review-automation/internal/validator"
	"strconv"
	"time"

	"github.com/tidwall/gjson"
)

// Processor defines the interface for processing pull requests
// Processor defines the interface for processing pull requests
type Processor interface {
	ProcessPullRequest(ctx context.Context, req *domain.ReviewRequest) error
}

// Reviewer defines the interface for reviewing pull requests
type Reviewer interface {
	ReviewPR(ctx context.Context, req *domain.ReviewRequest) (*domain.ReviewResult, error)
}

// Commenter defines the interface for posting comments
type Commenter interface {
	CallTool(ctx context.Context, serverName, toolName string, args map[string]interface{}) (any, error)
}

// PRProcessor handles processing of pull requests
type PRProcessor struct {
	cfg       *config.Config
	reviewer  Reviewer
	commenter Commenter
	storage   storage.Repository
	tracker   *syncutil.Tracker
}

// NewPRProcessor creates a new PR processor with dependencies injected
func NewPRProcessor(cfg *config.Config, reviewer Reviewer, commenter Commenter, storage storage.Repository, tracker *syncutil.Tracker) *PRProcessor {
	return &PRProcessor{
		cfg:       cfg,
		reviewer:  reviewer,
		commenter: commenter,
		storage:   storage,
		tracker:   tracker,
	}
}

// CreateBackupHandler creates a backup LLM handler function
func (p *PRProcessor) CreateBackupHandler(mcpClient *client.MCPClient, promptLoader *pipeline.PromptLoader, backupLLM pipeline.LLMClient) func(context.Context, *domain.ReviewRequest, int) error {
	if backupLLM == nil {
		return nil
	}

	backupAdapter := pipeline.NewPipelineAdapter(p.cfg, mcpClient, backupLLM, promptLoader)
	return func(ctx context.Context, req *domain.ReviewRequest, degradeLevel int) error {
		return p.processWithReviewer(ctx, req, backupAdapter)
	}
}

// ProcessPullRequest processes a pull request
func (p *PRProcessor) ProcessPullRequest(ctx context.Context, req *domain.ReviewRequest) error {
	start := time.Now()
	pr := req.PR
	slog.Debug("process pr", "id", pr.ID, "repo", pr.RepoSlug, "title", pr.Title)
	slog.Info("processing pr", "id", pr.ID)

	metrics.PullRequestTotal.WithLabelValues("started").Inc()

	// Parallel Fetching using errgroup
	g, gCtx := errgroup.WithContext(ctx)

	// Task 1: Fetch Existing AI Comments (Only if not already in req)
	if len(req.HistoricalComments) == 0 {
		g.Go(func() error {
			req.HistoricalComments = p.fetchExistingAIComments(gCtx, pr)
			return nil
		})
	}

	// Task 2: Fetch Diff (Wait, Stage 1 in Adapter fetches the diff for review,
	// but we also need it for validation at the end.
	// Let's keep fetching it here or cache it as well.)
	var diff string
	g.Go(func() error {
		diff = p.fetchDiff(gCtx, pr)
		return nil
	})

	// Wait for preparatory data
	if err := g.Wait(); err != nil {
		metrics.PullRequestTotal.WithLabelValues("failed_fetch").Inc()
		return fmt.Errorf("fetch data failed: %w", err)
	}

	// 4. Review PR (Main Critical Path)
	review, err := p.reviewer.ReviewPR(ctx, req)
	if err != nil {
		metrics.PullRequestTotal.WithLabelValues("failed_review").Inc()
		return fmt.Errorf("review pr: %w", err)
	}

	// 5. Validate and Filter Comments
	// Note: Diff availability checked in fetchDiff, if empty validator might be limited but won't crash
	commentValidator := validator.NewCommentValidator(diff)
	validComments, invalidComments := p.validateComments(review.Comments, commentValidator)

	// 6. Semantic Deduplication
	newComments := p.filterDuplicates(validComments, req.HistoricalComments)
	slog.Info("comment processing result",
		"original_count", len(review.Comments),
		"valid_count", len(validComments),
		"invalid_count", len(invalidComments),
		"filtered_count", len(newComments),
		"existing_count", len(req.HistoricalComments))
	review.Comments = newComments

	// 7. Async Persistence (Audit)
	if p.storage != nil {
		// Use Tracker to ensure this completes on shutdown
		p.tracker.Go(func() {
			// Create a detached context with timeout for saving
			saveCtx, cancel := context.WithTimeout(context.Background(), p.cfg.Storage.Timeout)
			defer cancel()

			record := &storage.ReviewRecord{
				ID:          fmt.Sprintf("%s-%s-%s-%d", pr.ProjectKey, pr.RepoSlug, pr.ID, time.Now().UnixNano()),
				PullRequest: pr,
				Result:      review,
				CreatedAt:   time.Now(),
				DurationMs:  time.Since(start).Milliseconds(),
				Status:      "success",
			}
			if err := p.storage.SaveReview(saveCtx, record); err != nil {
				slog.Warn("audit save failed", "error", err)
			}
		})
	}

	slog.Info("posting comments", "count", len(review.Comments))

	return p.postComments(ctx, pr, review, req.HistoricalComments, commentValidator)
}

// processWithReviewer processes the review request using a specific reviewer (backup or main)
// It skips the fetching steps as the request object is already populated.
func (p *PRProcessor) processWithReviewer(ctx context.Context, req *domain.ReviewRequest, specificReviewer Reviewer) error {
	start := time.Now()
	pr := req.PR
	reviewerName := "unknown"
	if named, ok := specificReviewer.(interface{ Name() string }); ok {
		reviewerName = named.Name()
	}
	slog.Info("processing pr with specific reviewer", "id", pr.ID, "reviewer", reviewerName)

	// 1. Review PR
	review, err := specificReviewer.ReviewPR(ctx, req)
	if err != nil {
		metrics.PullRequestTotal.WithLabelValues("failed_backup_review").Inc()
		return fmt.Errorf("backup review pr: %w", err)
	}

	// 2. Validate and Filter
	// Note: We need Diff for validation. Req doesn't store Diff string, only Changes/Context in cache.
	// We might need to re-fetch Diff string or cache it in Req.
	// For now, let's re-fetch diff as it's fast (MCP call) or rely on cached stage 1?
	// Stage 1 has Diff content (HunkLines). Validator needs raw diff string or parsed?
	// Validator.NewCommentValidator(diff).
	// Let's re-fetch diff for safety and simplicity, backup is rare.
	// 2. Validate and Filter
	diff := p.fetchDiff(ctx, pr)
	commentValidator := validator.NewCommentValidator(diff)

	validComments, _ := p.validateComments(review.Comments, commentValidator)

	// 3. Deduplication
	newComments := p.filterDuplicates(validComments, req.HistoricalComments)
	review.Comments = newComments

	// 4. Persistence (Skip for backup? Or keep audit?)
	// Keep audit for visibility.
	if p.storage != nil {
		p.tracker.Go(func() {
			saveCtx, cancel := context.WithTimeout(context.Background(), p.cfg.Storage.Timeout)
			defer cancel()
			record := &storage.ReviewRecord{
				ID:          fmt.Sprintf("%s-%s-%s-%d-backup", pr.ProjectKey, pr.RepoSlug, pr.ID, time.Now().UnixNano()),
				PullRequest: pr,
				Result:      review,
				CreatedAt:   time.Now(),
				DurationMs:  time.Since(start).Milliseconds(),
				Status:      "success_backup",
			}
			if err := p.storage.SaveReview(saveCtx, record); err != nil {
				slog.Warn("audit save failed", "error", err)
			}
		})
	}

	return p.postComments(ctx, pr, review, req.HistoricalComments, commentValidator)
}

// fetchDiff retrieves the PR diff from Bitbucket for comment validation
func (p *PRProcessor) fetchDiff(ctx context.Context, pr *domain.PullRequest) string {
	prID, _ := strconv.Atoi(pr.ID)
	result, err := p.commenter.CallTool(ctx, config.MCPServerBitbucket, config.ToolBitbucketGetDiff, map[string]interface{}{
		"projectKey":    pr.ProjectKey,
		"repoSlug":      pr.RepoSlug,
		"pullRequestId": prID,
	})
	if err != nil {
		slog.Warn("fetch diff failed", "error", err)
		return ""
	}

	// Handle different result types
	if s, ok := result.(string); ok {
		return s
	}

	// Try to extract from MCP content structure
	jsonBytes, err := json.Marshal(result)
	if err != nil {
		return ""
	}
	res := gjson.GetBytes(jsonBytes, "content.0.text").String()
	if res == "" {
		// Fallback to "output" field (common in some ADK tools)
		res = gjson.GetBytes(jsonBytes, "output").String()
	}

	// [FIX] Handle case where the text result itself is a JSON string containing "diff"
	// This happens with some Bitbucket MCP servers that return {"diff": "..."} as the text content
	if len(res) > 0 && res[0] == '{' {
		diffField := gjson.Get(res, "diff")
		if diffField.Exists() {
			return diffField.String()
		}
	}
	return res
}
