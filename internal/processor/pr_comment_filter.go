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
	for _, c := range comments {
		if c.File == "" || c.Line == 0 {
			// General comment (no file/line) - always valid
			valid = append(valid, c)
			continue
		}

		// STRICT VALIDATION: Always ensure comment is on a valid diff line
		if v.IsValid(c.File, int(c.Line)) {
			valid = append(valid, c)
		} else {
			reason := v.GetInvalidReason(c.File, int(c.Line))
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

			// Check if content contains a table (Merged Comment)
			tableComments := parseTableComments(rawContent)
			if len(tableComments) > 0 {
				comments = append(comments, tableComments...)
			}

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

			// Identify if this is a legacy/individual comment (not table)
			if len(tableComments) == 0 && path != "" {
				// Capture marker
				var marker string
				if start := strings.Index(rawContent, config.MarkerAIReviewPrefix); start != -1 {
					if end := strings.Index(rawContent[start:], config.MarkerAIReviewSuffix); end != -1 {
						marker = rawContent[start : start+end+len(config.MarkerAIReviewSuffix)]
					}
				}

				comments = append(comments, domain.ReviewComment{
					File:    path,
					Line:    domain.FlexibleLine(line),
					Comment: cleanComment,
					Marker:  marker,
				})
			}
		}
		return true // keep iterating
	})

	return comments
}

// parseTableComments extracts comments from Markdown tables in the message
func parseTableComments(content string) []domain.ReviewComment {
	var comments []domain.ReviewComment

	// Check for file path in header/marker
	// Default file from marker if present e.g. <!-- ai-review::file:src/main.go:commit -->
	var defaultFile string
	if start := strings.Index(content, config.MarkerAIReviewPrefix+config.MarkerTypeFile+":"); start != -1 {
		rest := content[start+len(config.MarkerAIReviewPrefix+config.MarkerTypeFile+":"):]
		if idx := strings.Index(rest, ":"); idx != -1 {
			defaultFile = rest[:idx]
		}
	}

	lines := strings.Split(content, "\n")
	inTable := false
	tableType := "" // "file" or "summary"

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		// Detect table header
		if strings.Contains(line, "| Line | Severity | Message |") {
			inTable = true
			tableType = "file"
			continue
		}
		if strings.Contains(line, "| File | Line | Suggestion |") {
			inTable = true
			tableType = "summary"
			continue
		}
		if strings.Contains(line, "|------|") {
			continue
		}

		if inTable && strings.HasPrefix(line, "|") {
			parts := strings.Split(line, "|")
			if len(parts) < 4 {
				continue // Invalid row
			}

			// Parse row based on type
			// parts[0] is empty (before first |)
			switch tableType {
			case "file":
				// | Line | Severity | Message |
				// parts: [ "", " 12 ", " INFO ", " msg ", "" ]
				if len(parts) >= 4 {
					lineNum, _ := strconv.Atoi(strings.TrimSpace(parts[1]))
					severity := strings.TrimSpace(parts[2]) // Badge might contain emoji
					msg := strings.TrimSpace(parts[3])

					// Clean severity (remove emoji and extra text)
					if strings.Contains(severity, "WARNING") {
						severity = "WARNING"
					} else if strings.Contains(severity, "CRITICAL") {
						severity = "CRITICAL"
					} else {
						severity = "INFO"
					}

					comments = append(comments, domain.ReviewComment{
						File:     defaultFile,
						Line:     domain.FlexibleLine(lineNum),
						Comment:  msg,
						Severity: severity,
					})
				}
			case "summary":
				// | File | Line | Suggestion |
				if len(parts) >= 4 {
					// Use regex or simple parsing to extract file path from link if present
					// Link format: [path](url) or just path
					fileRaw := strings.TrimSpace(parts[1])
					file := extractPathFromLink(fileRaw)

					lineRaw := strings.TrimSpace(parts[2])
					lineNum := extractLineFromLink(lineRaw)

					msg := strings.TrimSpace(parts[3])

					comments = append(comments, domain.ReviewComment{
						File:    file,
						Line:    domain.FlexibleLine(lineNum),
						Comment: msg,
					})
				}
			}
		} else if inTable && !strings.HasPrefix(line, "|") {
			// End of table
			inTable = false
		}
	}
	return comments
}

func extractPathFromLink(text string) string {
	// [path](url) -> path
	if strings.HasPrefix(text, "[") {
		if end := strings.Index(text, "]"); end != -1 {
			return text[1:end]
		}
	}
	return text
}

func extractLineFromLink(text string) int {
	// [123](url) -> 123
	if strings.HasPrefix(text, "[") {
		if end := strings.Index(text, "]"); end != -1 {
			val := text[1:end]
			if i, err := strconv.Atoi(val); err == nil {
				return i
			}
		}
	}
	// Just 123
	if i, err := strconv.Atoi(text); err == nil {
		return i
	}
	return 0
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
