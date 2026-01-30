package processor

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	// "pr-review-automation/internal/agent" // Removed agent dependency for types
	"pr-review-automation/internal/config"
	"pr-review-automation/internal/domain"
	"pr-review-automation/internal/metrics"
	"pr-review-automation/internal/storage"
	"pr-review-automation/internal/validator"
	"strconv"
	"time"

	"github.com/tidwall/gjson"
)

// Processor defines the interface for processing pull requests
type Processor interface {
	ProcessPullRequest(ctx context.Context, pr *domain.PullRequest) error
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
}

// NewPRProcessor creates a new PR processor with dependencies injected
func NewPRProcessor(cfg *config.Config, reviewer Reviewer, commenter Commenter, storage storage.Repository) *PRProcessor {
	return &PRProcessor{
		cfg:       cfg,
		reviewer:  reviewer,
		commenter: commenter,
		storage:   storage,
	}
}

// ProcessPullRequest processes a pull request
func (p *PRProcessor) ProcessPullRequest(ctx context.Context, pr *domain.PullRequest) error {
	start := time.Now()
	slog.Debug("process pr", "id", pr.ID, "repo", pr.RepoSlug, "title", pr.Title)
	slog.Info("processing pr", "id", pr.ID)

	metrics.PullRequestTotal.WithLabelValues("started").Inc()

	// 1. Fetch Existing AI Comments (Bitbucket Native Dedup)
	existingComments := p.fetchExistingAIComments(ctx, pr)

	// 2. Build Review Request
	req := &domain.ReviewRequest{
		PR:                 pr,
		HistoricalComments: existingComments,
	}

	// 3. Review PR
	review, err := p.reviewer.ReviewPR(ctx, req)
	if err != nil {
		metrics.PullRequestTotal.WithLabelValues("failed").Inc()
		return fmt.Errorf("review pr: %w", err)
	}

	// 4. Fetch Diff for Validation
	diff := p.fetchDiff(ctx, pr)
	commentValidator := validator.NewCommentValidator(diff)

	// 5. Validate and Filter Comments
	validComments, invalidComments := p.validateComments(review.Comments, commentValidator)

	// 6. Semantic Deduplication
	newComments := p.filterDuplicates(validComments, existingComments)
	slog.Info("comment processing result",
		"original_count", len(review.Comments),
		"valid_count", len(validComments),
		"invalid_count", len(invalidComments),
		"filtered_count", len(newComments),
		"existing_count", len(existingComments))
	review.Comments = newComments

	// Persist review result (Audit Only)
	if p.storage != nil {
		// Save synchronously to ensure data safety on exit
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
	}

	slog.Info("posting comments", "count", len(review.Comments))

	return p.postComments(ctx, pr, review, existingComments, commentValidator)
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
