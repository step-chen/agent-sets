package processor

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"strings"

	"pr-review-automation/internal/config"
	"pr-review-automation/internal/domain"
	"pr-review-automation/internal/metrics"
	"pr-review-automation/internal/validator"

	"golang.org/x/sync/errgroup"
)

func (p *PRProcessor) postComments(ctx context.Context, pr *domain.PullRequest, review *domain.ReviewResult, existingComments []domain.ReviewComment, validator *validator.CommentValidator) error {
	if p.cfg.Pipeline.CommentMerge.Enabled {
		return p.postMergedComments(ctx, pr, review, existingComments, validator)
	}
	_ = p.postIndividualComments(ctx, pr, review.Comments, validator, review.Model)
	return p.postSummaryComment(ctx, pr, review, existingComments, "")
}

func (p *PRProcessor) postMergedComments(ctx context.Context, pr *domain.PullRequest, review *domain.ReviewResult, existingComments []domain.ReviewComment, validator *validator.CommentValidator) error {
	merger := NewCommentMerger(&p.cfg.Pipeline.CommentMerge, pr.WebURL, p.templates, p.cfg.Pipeline.AntiHallucination.ShowConfidence)
	result := merger.Merge(review.Comments, pr.LatestCommit)

	pullRequestId, _ := strconv.Atoi(pr.ID)

	// 1. Post file-level comments
	// Filter existing file comments
	toPostFiles := p.filterExistingFileComments(existingComments, result.FileComments, pr.LatestCommit)

	for _, fc := range toPostFiles {
		fc.ModelName = review.Model
		commentText := merger.FormatFileComment(&fc)
		commentText = TransformLinks(commentText, pr.WebURL)

		args := map[string]interface{}{
			"projectKey":    pr.ProjectKey,
			"repoSlug":      pr.RepoSlug,
			"pullRequestId": pullRequestId,
			"commentText":   commentText,
		}
		// File comments are general comments on the PR, but maybe we can attach to file level?
		// Bitbucket API supports file path for general file comments usually?
		// bitbucket_add_pull_request_comment can take 'filePath'.
		// If we provide filePath but no line, it's a file comment.
		if fc.FilePath != "" {
			args["filePath"] = fc.FilePath
			// For general file comments (no specific line), we don't strictly need lineType,
			// but if we want to be safe or if it's attached to the first line:
			// Let's check if the file is in diff. If so, we can try to attach to line 1 or just leave as file comment.
			// The original logic just added "ADDED" for file comments. Let's make it smarter or keep it safe.
			// If it's a file comment without line number, lineType might not be relevant or "CONTEXT" is fine.
			// But wait, the previous fix simply added "ADDED" effectively.
			// Let's use validator to check if file exists in diff.
			if validator != nil {
				// Check if file is in diff
				if validator.FileInDiff(fc.FilePath) {
					// If it is in diff, "ADDED" is usually safe for new files, but for modified files?
					// Actually, for file-level comments, Bitbucket might not require lineType if line is not set.
					// But if we want to be consistent:
					args["lineType"] = "ADDED" // Defaulting to ADDED as per previous fix for safety on new files.
				}
			} else {
				args["lineType"] = "ADDED" // Fallback
			}
		}

		slog.Debug("post merged file comment", "file", fc.FilePath)
		_, err := p.commenter.CallTool(ctx, config.MCPServerBitbucket, config.ToolBitbucketAddComment, args)
		if err != nil {
			slog.Error("post merged comment failed", "file", fc.FilePath, "error", err)
			metrics.CommentPostFailures.WithLabelValues("api_error").Inc()
		}
	}

	// 1b. Post individual (NotMerged) high-severity comments (Hybrid Mode)
	// Filter those first
	toPostIndividual := p.filterDuplicates(result.NotMerged, existingComments)
	if len(toPostIndividual) > 0 {
		slog.Debug("post hybrid individual comments", "count", len(toPostIndividual))
		if err := p.postIndividualComments(ctx, pr, toPostIndividual, validator, review.Model); err != nil {
			slog.Error("post hybrid individual comments failed", "error", err)
		}
	}

	// 2. Post summary with INFO/NIT appended
	addonsText := merger.FormatSummaryAddons(result.SummaryAddons)
	return p.postSummaryComment(ctx, pr, review, existingComments, addonsText)
}

func (p *PRProcessor) postIndividualComments(ctx context.Context, pr *domain.PullRequest, comments []domain.ReviewComment, validator *validator.CommentValidator, modelName string) error {
	pullRequestId, err := strconv.Atoi(pr.ID)
	if err != nil {
		return fmt.Errorf("invalid pr id: %s", pr.ID)
	}

	// Use errgroup to post comments in parallel
	limit := p.cfg.Pipeline.MaxConcurrentComments
	if limit <= 0 {
		limit = 5
	}
	g, gCtx := errgroup.WithContext(ctx)
	g.SetLimit(limit)

	for _, comment := range comments {
		comment := comment
		g.Go(func() error {
			content := comment.Comment
			// if p.cfg.Pipeline.AntiHallucination.ShowConfidence {
			// 	content += fmt.Sprintf("\n\n*Confidence: %.0f%%*", comment.Confidence*100)
			// }

			// Construct data for template
			data := InlineTemplateData{
				MarkerData: MarkerData{
					Type:   comment.File, // Legacy individual format uses raw filename as first segment
					Line:   int(comment.Line),
					Commit: pr.LatestCommit,
				},
				FooterData: FooterData{
					Model:          modelName,
					Confidence:     comment.Confidence * 100,
					ShowConfidence: p.cfg.Pipeline.AntiHallucination.ShowConfidence,
				},
				Content: content,
			}

			// Render comment
			var buf bytes.Buffer
			if err := p.templates.Inline.Execute(&buf, data); err != nil {
				slog.Error("render comment failed", "error", err)
				return err
			}
			commentText := TransformLinks(buf.String(), pr.WebURL)

			args := map[string]interface{}{
				"projectKey":    pr.ProjectKey,
				"repoSlug":      pr.RepoSlug,
				"pullRequestId": pullRequestId,
				"commentText":   commentText,
			}

			if comment.File != "" {
				args["filePath"] = comment.File

				// Determine line type dynamically
				lineType := "ADDED" // Default fallback
				if validator != nil {
					lt := validator.GetLineType(comment.File, int(comment.Line))
					if lt != "" {
						lineType = lt
					}
				}
				args["lineType"] = lineType

				if comment.Line > 0 {
					args["lineNumber"] = strconv.Itoa(int(comment.Line))
				}
			}

			slog.Debug("post comment", "file", comment.File, "line", int(comment.Line))
			_, err := p.commenter.CallTool(gCtx, config.MCPServerBitbucket, config.ToolBitbucketAddComment, args)
			if err != nil {
				slog.Error("post comment failed", "file", comment.File, "error", err)
				metrics.CommentPostFailures.WithLabelValues("api_error").Inc()
				return nil
			}
			return nil
		})
	}

	if err := g.Wait(); err != nil {
		slog.Warn("some comments failed to post", "error", err)
	}
	return p.cleanupSession(pr.ID)
}

// postSummaryComment posts a standalone summary comment to the PR
func (p *PRProcessor) postSummaryComment(ctx context.Context, pr *domain.PullRequest, review *domain.ReviewResult, existingComments []domain.ReviewComment, addonsText string) error {
	if p.hasExistingSummary(existingComments, pr.LatestCommit) {
		slog.Info("summary already exists", "commit", pr.LatestCommit)
		return p.cleanupSession(pr.ID)
	}

	pullRequestId, _ := strconv.Atoi(pr.ID)
	summaryText := cleanSummaryMarkdown(review.Summary)

	data := SummaryTemplateData{
		MarkerData: MarkerData{
			Type:   config.MarkerTypeSummary,
			Commit: pr.LatestCommit,
		},
		FooterData: FooterData{
			Model:          review.Model,
			ShowConfidence: false, // Summary score is separate
		},
		Score:   review.Score,
		Content: summaryText,
		Addons:  addonsText,
	}

	var buf bytes.Buffer
	if err := p.templates.Summary.Execute(&buf, data); err != nil {
		return fmt.Errorf("render summary failed: %w", err)
	}

	fullSummary := TransformLinks(buf.String(), pr.WebURL)

	args := map[string]interface{}{
		"projectKey":    pr.ProjectKey,
		"repoSlug":      pr.RepoSlug,
		"pullRequestId": pullRequestId,
		"commentText":   fullSummary,
	}

	_, err := p.commenter.CallTool(ctx, config.MCPServerBitbucket, config.ToolBitbucketAddComment, args)
	if err != nil {
		slog.Error("post summary failed", "error", err)
		metrics.CommentPostFailures.WithLabelValues("summary_error").Inc()
	}
	return p.cleanupSession(pr.ID)
}

func (p *PRProcessor) cleanupSession(prID string) error {
	if cleaner, ok := p.commenter.(interface{ ClearSessionHistory(string) }); ok {
		cleaner.ClearSessionHistory("pr-" + prID)
	}
	return nil
}

// cleanSummaryMarkdown removes markdown formatting to produce plain text
func cleanSummaryMarkdown(summary string) string {
	lines := strings.Split(summary, "\n")
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)

		// Remove headers (start of line)
		if strings.HasPrefix(trimmed, "#") {
			content := strings.TrimLeft(trimmed, "#")
			trimmed = strings.TrimSpace(content)
		}

		// Remove code blocks/ticks
		trimmed = strings.ReplaceAll(trimmed, "```", "")
		trimmed = strings.ReplaceAll(trimmed, "`", "")

		// Remove bold markers
		trimmed = strings.ReplaceAll(trimmed, "**", "")
		trimmed = strings.ReplaceAll(trimmed, "__", "")

		// Optional: Remove common list markers if needed,
		// but preserve indentation/vibe for readability.
		trimmed = strings.TrimPrefix(trimmed, "* ")

		lines[i] = trimmed
	}
	return strings.Join(lines, "\n")
}
