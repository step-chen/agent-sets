package processor

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"pr-review-automation/internal/agent"
	"pr-review-automation/internal/domain"
	"pr-review-automation/internal/metrics"
	"pr-review-automation/internal/storage"
	"pr-review-automation/internal/validator"
	"strconv"
	"strings"
	"time"

	"github.com/tidwall/gjson"
	"golang.org/x/sync/errgroup"
)

// Processor defines the interface for processing pull requests
type Processor interface {
	ProcessPullRequest(ctx context.Context, pr *domain.PullRequest) error
}

// Reviewer defines the interface for reviewing pull requests
type Reviewer interface {
	ReviewPR(ctx context.Context, req *agent.ReviewRequest) (*agent.ReviewResult, error)
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

	metrics.PullRequestTotal.WithLabelValues("started").Inc()

	// 1. Fetch Existing AI Comments (Bitbucket Native Dedup)
	existingComments := p.fetchExistingAIComments(ctx, pr)

	// 2. Build Review Request
	req := &agent.ReviewRequest{
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
		// Async save to not block main flow
		go func() {
			saveCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
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
		}()
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
				"commentText":   fmt.Sprintf("<!-- ai-review:%s:%d:%s -->\n%s", comment.File, comment.Line, pr.LatestCommit, comment.Comment),
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

// fetchExistingAIComments fetches existing comments from Bitbucket and filters for AI comments
func (p *PRProcessor) fetchExistingAIComments(ctx context.Context, pr *domain.PullRequest) []agent.ReviewComment {
	// Call bitbucket_get_pull_request_comments
	// Convert PR ID to int
	prID, _ := strconv.Atoi(pr.ID)
	result, err := p.commenter.CallTool(ctx, "bitbucket", "bitbucket_get_pull_request_comments", map[string]interface{}{
		"projectKey":    pr.ProjectKey,
		"repoSlug":      pr.RepoSlug,
		"pullRequestId": prID,
	})
	if err != nil {
		slog.Warn("fetch existing comments failed", "error", err)
		return nil
	}

	// Marshaling result to JSON to parse with gjson
	jsonBytes, err := json.Marshal(result)
	if err != nil {
		slog.Warn("marshal comments failed", "error", err)
		return nil
	}
	jsonStr := string(jsonBytes)

	var comments []agent.ReviewComment

	// Parse using gjson
	// Assuming structure: { "values": [ { "content": { "raw": "..." }, "inline": { "path": "...", "from": 123 } } ] }
	gjson.Get(jsonStr, "values").ForEach(func(key, value gjson.Result) bool {
		rawContent := value.Get("content.raw").String()

		// Check for AI marker
		if strings.Contains(rawContent, "<!-- ai-review:") || strings.Contains(rawContent, "**AI Review**") {
			path := value.Get("inline.path").String()
			// 'to' is usually the line number in PR diffs for added/modified lines in Bitbucket
			line := int(value.Get("inline.to").Int())

			// If path/line not in inline (e.g. general comment), try to parse from marker
			if path == "" {
				// Parse from marker: <!-- ai-review:file:line -->
				if start := strings.Index(rawContent, "<!-- ai-review:"); start != -1 {
					end := strings.Index(rawContent[start:], "-->")
					if end != -1 {
						marker := rawContent[start : start+end]
						parts := strings.Split(marker, ":")
						if len(parts) >= 3 {
							path = parts[1]
							if l, err := strconv.Atoi(parts[2]); err == nil {
								line = l
							}
						}
					}
				}
			}

			// Clean comment content (remove marker)
			cleanComment := rawContent
			// Remove HTML comments
			if idx := strings.Index(cleanComment, "-->"); idx != -1 {
				cleanComment = strings.TrimSpace(cleanComment[idx+3:])
			}

			if path != "" {
				comments = append(comments, agent.ReviewComment{
					File:    path,
					Line:    line,
					Comment: cleanComment,
				})
			}
		}
		return true // keep iterating
	})

	return comments
}

// filterDuplicates filters out comments that have already been made
func (p *PRProcessor) filterDuplicates(newComments, existingComments []agent.ReviewComment) []agent.ReviewComment {
	if len(existingComments) == 0 {
		return newComments
	}

	existingSet := make(map[string]bool)
	for _, c := range existingComments {
		fp := p.semanticFingerprint(c)
		existingSet[fp] = true
	}

	var filtered []agent.ReviewComment
	for _, c := range newComments {
		if !existingSet[p.semanticFingerprint(c)] {
			filtered = append(filtered, c)
		}
	}
	return filtered
}

// semanticFingerprint creates a fingerprint for a comment based on file and content keywords
func (p *PRProcessor) semanticFingerprint(c agent.ReviewComment) string {
	// Simple fingerprint: file + first 50 chars of comment (lowercase)
	// This avoids line number dependency
	content := strings.ToLower(strings.TrimSpace(c.Comment))
	if len(content) > 50 {
		content = content[:50]
	}
	return fmt.Sprintf("%s:%s", c.File, content)
}

// fetchDiff retrieves the PR diff from Bitbucket for comment validation
func (p *PRProcessor) fetchDiff(ctx context.Context, pr *domain.PullRequest) string {
	prID, _ := strconv.Atoi(pr.ID)
	result, err := p.commenter.CallTool(ctx, "bitbucket", "bitbucket_get_pull_request_diff", map[string]interface{}{
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
	return res
}

// validateComments validates comments against diff ranges
// Returns valid comments and invalid comments (for potential general comment conversion)
func (p *PRProcessor) validateComments(comments []agent.ReviewComment, v *validator.CommentValidator) (valid, invalid []agent.ReviewComment) {
	for _, c := range comments {
		if c.File == "" || c.Line == 0 {
			// General comment (no file/line) - always valid
			valid = append(valid, c)
			continue
		}

		if v.IsValid(c.File, c.Line) {
			valid = append(valid, c)
		} else {
			reason := v.GetInvalidReason(c.File, c.Line)
			slog.Warn("invalid comment line",
				"file", c.File,
				"line", c.Line,
				"reason", reason)
			invalid = append(invalid, c)
		}
	}
	return
}
