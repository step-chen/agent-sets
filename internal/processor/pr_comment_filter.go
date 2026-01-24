package processor

import (
	"context"
	"encoding/json"
	"log/slog"
	"strconv"
	"strings"

	"pr-review-automation/internal/config"
	"pr-review-automation/internal/domain"
	"pr-review-automation/internal/validator"

	"github.com/tidwall/gjson"
)

// validateComments validates comments against diff ranges
func (p *PRProcessor) validateComments(comments []domain.ReviewComment, v *validator.CommentValidator) (valid, invalid []domain.ReviewComment) {
	mergeEnabled := p.cfg.Pipeline.CommentMerge.Enabled
	for _, c := range comments {
		if c.File == "" || c.Line == 0 {
			// General comment (no file/line) - always valid
			valid = append(valid, c)
			continue
		}

		// If merge is enabled, comments are grouped by file and not strictly tied to inline diff lines.
		// We only require that the file itself is part of the PR diff.
		if mergeEnabled {
			if v.FileInDiff(c.File) {
				valid = append(valid, c)
			} else {
				slog.Warn("invalid comment file (merging enabled)",
					"file", c.File,
					"reason", "file not in diff")
				invalid = append(invalid, c)
			}
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

// filterDuplicates filters out comments that have already been made
func (p *PRProcessor) filterDuplicates(newComments, existingComments []domain.ReviewComment) []domain.ReviewComment {
	if len(existingComments) == 0 {
		return newComments
	}

	existingSet := make(map[string]bool)
	for _, c := range existingComments {
		fp := c.Fingerprint()
		existingSet[fp] = true
	}

	var filtered []domain.ReviewComment
	for _, c := range newComments {
		if !existingSet[c.Fingerprint()] {
			filtered = append(filtered, c)
		}
	}
	return filtered
}

// fetchExistingAIComments fetches existing comments from Bitbucket and filters for AI comments
func (p *PRProcessor) fetchExistingAIComments(ctx context.Context, pr *domain.PullRequest) []domain.ReviewComment {
	// Call bitbucket_get_pull_request_comments
	// Convert PR ID to int
	prID, _ := strconv.Atoi(pr.ID)
	result, err := p.commenter.CallTool(ctx, config.MCPServerBitbucket, config.ToolBitbucketGetComments, map[string]interface{}{
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

	var comments []domain.ReviewComment

	// Parse using gjson
	// Assuming structure: { "values": [ { "content": { "raw": "..." }, "inline": { "path": "...", "from": 123 } } ] }
	gjson.Get(jsonStr, "values").ForEach(func(key, value gjson.Result) bool {
		rawContent := value.Get("content.raw").String()

		// Check for AI marker
		if strings.Contains(rawContent, config.MarkerAIReviewPrefix) || strings.Contains(rawContent, config.MarkerAIReviewVisible) {
			path := value.Get("inline.path").String()
			// 'to' is usually the line number in PR diffs for added/modified lines in Bitbucket
			line := int(value.Get("inline.to").Int())

			// If path/line not in inline (e.g. general comment), try to parse from marker
			if path == "" {
				// Parse from marker: <!-- ai-review:file:line -->
				if start := strings.Index(rawContent, config.MarkerAIReviewPrefix); start != -1 {
					end := strings.Index(rawContent[start:], config.MarkerAIReviewSuffix)
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
			if idx := strings.Index(cleanComment, config.MarkerAIReviewSuffix); idx != -1 {
				cleanComment = strings.TrimSpace(cleanComment[idx+len(config.MarkerAIReviewSuffix):])
			}

			if path != "" {
				// Capture marker
				var marker string
				if start := strings.Index(rawContent, config.MarkerAIReviewPrefix); start != -1 {
					if end := strings.Index(rawContent[start:], config.MarkerAIReviewSuffix); end != -1 {
						marker = rawContent[start : start+end+len(config.MarkerAIReviewSuffix)]
					}
				}

				comments = append(comments, domain.ReviewComment{
					File:    path,
					Line:    line,
					Comment: cleanComment,
					Marker:  marker,
				})
			}
		}
		return true // keep iterating
	})

	return comments
}

// hasExistingSummary checks if a summary comment exists for the commit
func (p *PRProcessor) hasExistingSummary(comments []domain.ReviewComment, commit string) bool {
	for _, c := range comments {
		_, _, markerCommit, found := parseMarker(c.Marker)
		if found && markerTypeFromMarker(c.Marker) == config.MarkerTypeSummary && markerCommit == commit {
			return true
		}
	}
	return false
}

// filterExistingFileComments checks if file-level comments already exist
func (p *PRProcessor) filterExistingFileComments(
	existingComments []domain.ReviewComment,
	mergedComments []MergedFileComment,
	commit string,
) []MergedFileComment {
	existingFiles := make(map[string]bool)
	for _, c := range existingComments {
		if c.Marker == "" {
			continue
		}
		mType, key, mCommit, found := parseMarker(c.Marker)
		if found && mType == config.MarkerTypeFile && mCommit == commit {
			existingFiles[key] = true
		}
	}

	var toPost []MergedFileComment
	for _, fc := range mergedComments {
		if !existingFiles[fc.FilePath] {
			toPost = append(toPost, fc)
		} else {
			slog.Info("skipping existing file comment", "file", fc.FilePath)
		}
	}
	return toPost
}

func markerTypeFromMarker(text string) string {
	if strings.Contains(text, config.MarkerTypeSummary) {
		return config.MarkerTypeSummary
	}
	if strings.Contains(text, config.MarkerTypeFile) {
		return config.MarkerTypeFile
	}
	return ""
}

// parseMarker extracts marker type, key, and commit from comment text
func parseMarker(text string) (mType, key, commit string, found bool) {
	// MarkerPrefixFile = "<!-- ai-review::file:"
	filePrefix := config.MarkerAIReviewPrefix + config.MarkerTypeFile + ":"
	summaryPrefix := config.MarkerAIReviewPrefix + config.MarkerTypeSummary + ":"

	if strings.HasPrefix(text, filePrefix) {
		// e.g. "path:commit -->"
		content := text[len(filePrefix):]
		if idx := strings.Index(content, config.MarkerAIReviewSuffix); idx != -1 {
			content = content[:idx]
			// content is "path:commit"
			lastColon := strings.LastIndex(content, ":")
			if lastColon != -1 {
				mType = config.MarkerTypeFile
				key = content[:lastColon]
				commit = content[lastColon+1:]
				found = true
			}
		}
	} else if strings.HasPrefix(text, summaryPrefix) {
		// e.g. "commit -->"
		content := text[len(summaryPrefix):]
		if idx := strings.Index(content, config.MarkerAIReviewSuffix); idx != -1 {
			commit = content[:idx]
			mType = config.MarkerTypeSummary
			key = "summary"
			found = true
		}
	}
	return
}
