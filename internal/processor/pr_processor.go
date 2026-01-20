package processor

import (
	"context"
	"fmt"
	"log/slog"

	"pr-review-automation/internal/agent"
	"pr-review-automation/internal/domain"
	"pr-review-automation/internal/metrics"
	"pr-review-automation/internal/storage"
	"strconv"
	"time"

	"golang.org/x/sync/errgroup"
)

// Processor defines the interface for processing pull requests
type Processor interface {
	ProcessPullRequest(ctx context.Context, pr *domain.PullRequest) error
}

// Reviewer defines the interface for reviewing pull requests
type Reviewer interface {
	ReviewPR(ctx context.Context, pr *domain.PullRequest) (*agent.ReviewResult, error)
}

// Commenter defines the interface for posting comments
type Commenter interface {
	CallTool(ctx context.Context, serverName, toolName string, args map[string]interface{}) (any, error)
}

// PRProcessor handles processing of pull requests
type PRProcessor struct {
	reviewer  Reviewer
	commenter Commenter
	storage   storage.Repository
}

// NewPRProcessor creates a new PR processor with dependencies injected
func NewPRProcessor(reviewer Reviewer, commenter Commenter, storage storage.Repository) *PRProcessor {
	return &PRProcessor{
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

	// Use the agent to review the PR

	review, err := p.reviewer.ReviewPR(ctx, pr)
	if err != nil {
		metrics.PullRequestTotal.WithLabelValues("failed").Inc()
		return fmt.Errorf("review pr: %w", err)
	}

	// Persist review result
	if p.storage != nil {
		record := &storage.ReviewRecord{
			ID:          fmt.Sprintf("%s-%s-%s-%d", pr.ProjectKey, pr.RepoSlug, pr.ID, time.Now().UnixNano()),
			PullRequest: pr,
			Result:      review,
			CreatedAt:   time.Now(),
			DurationMs:  time.Since(start).Milliseconds(),
			Status:      "success",
		}
		if err := p.storage.SaveReview(ctx, record); err != nil {
			slog.Error("save review failed", "error", err)
			// Non-blocking: continue even if storage fails
		} else {
			slog.Debug("review saved", "id", record.ID)
		}
	}

	slog.Info("posting comments", "count", len(review.Comments))

	pullRequestId, err := strconv.Atoi(pr.ID)
	if err != nil {
		slog.Error("invalid pr id", "pr_id", pr.ID)
		return fmt.Errorf("invalid pr id: %s", pr.ID)
	}

	// Use errgroup to post comments in parallel
	// Limit concurrency to avoid overwhelming Bitbucket API
	const maxConcurrentComments = 5
	g, gCtx := errgroup.WithContext(ctx)
	g.SetLimit(maxConcurrentComments)

	for _, comment := range review.Comments {
		// Capture loop variable
		comment := comment
		g.Go(func() error {
			args := map[string]interface{}{
				"projectKey":    pr.ProjectKey,
				"repoSlug":      pr.RepoSlug,
				"pullRequestId": pullRequestId,
				"commentText":   comment.Comment,
			}

			if comment.File != "" {
				args["filePath"] = comment.File
				if comment.Line > 0 {
					args["lineNumber"] = comment.Line
				}
			}

			// Use gCtx for cancellation propagation, but be careful if gCtx is cancelled by one failure
			// we might want independent failures. But errgroup cancels all on first error by default.
			slog.Debug("post comment", "file", comment.File, "line", comment.Line)
			_, err := p.commenter.CallTool(gCtx, "bitbucket", "bitbucket_add_pull_request_comment", args)
			if err != nil {
				slog.Error("post comment failed",
					"pr_id", pr.ID,
					"file", comment.File,
					"error", err)
				metrics.CommentPostFailures.WithLabelValues("api_error").Inc()
				// Return nil to allow other comments to proceed (Best Effort)
				return nil
			}
			return nil
		})
	}

	// Wait for all comments to be posted
	g.Wait()

	// Post summary comment (more important than individual comments)
	if review.Summary != "" {
		args := map[string]interface{}{
			"projectKey":    pr.ProjectKey,
			"repoSlug":      pr.RepoSlug,
			"pullRequestId": pullRequestId,
			"commentText":   fmt.Sprintf("**AI Review Summary (Model: %s)**\nScore: %d\n\n%s", review.Model, review.Score, review.Summary),
		}

		_, err := p.commenter.CallTool(ctx, "bitbucket", "bitbucket_add_pull_request_comment", args)
		if err != nil {
			slog.Error("post summary failed",
				"pr_id", pr.ID,
				"error", err)
			// Track summary failures separately as they're more critical than individual comments
			metrics.CommentPostFailures.WithLabelValues("summary_error").Inc()
		}
	}

	return nil
}
