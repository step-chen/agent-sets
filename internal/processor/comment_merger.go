package processor

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"pr-review-automation/internal/config"
	"pr-review-automation/internal/domain"
)

// CommentMerger handles comment grouping and merging
type CommentMerger struct {
	config   *config.CommentMergeConfig
	prWebURL string
}

// NewCommentMerger creates a new CommentMerger
func NewCommentMerger(cfg *config.CommentMergeConfig, prWebURL string) *CommentMerger {
	return &CommentMerger{config: cfg, prWebURL: prWebURL}
}

// MergeResult contains merged comments ready for posting
type MergeResult struct {
	FileComments  []MergedFileComment
	SummaryAddons []domain.ReviewComment // INFO/NIT to append to summary
	NotMerged     []domain.ReviewComment // Comments to post individually (Hybrid Mode)
}

// MergedFileComment represents a merged comment for a single file
type MergedFileComment struct {
	FilePath  string
	Comments  []domain.ReviewComment
	Marker    string // <!-- ai-review::file:path:commit -->
	ModelName string
}

// Merge groups and merges comments by severity and file
func (m *CommentMerger) Merge(comments []domain.ReviewComment, commit string) *MergeResult {
	res := &MergeResult{
		FileComments:  make([]MergedFileComment, 0),
		SummaryAddons: make([]domain.ReviewComment, 0),
	}

	if !m.config.Enabled {
		return res
	}

	fileGroups := make(map[string][]domain.ReviewComment)

	for _, c := range comments {
		isHighSeverity := m.isHighSeverity(c.Severity)

		if isHighSeverity {
			switch m.config.HighSeverityMerge {
			case "by_file":
				// Use file path or fallback to "General" if empty, though comments should have file
				key := c.File
				if key == "" {
					key = "General"
				}
				fileGroups[key] = append(fileGroups[key], c)
			case "none":
				// INDIVIDUAL MODE: Treat as individual file comments but pass through results
				// Actually, if it's "none", we don't want them in fileGroups for merging.
				// We want them as separate entities.
				// Let's add a list of NotMerged comments to MergeResult.
				res.NotMerged = append(res.NotMerged, c)
			default:
				// Fallback to "by_file" behavior if not specified
				if c.File != "" {
					fileGroups[c.File] = append(fileGroups[c.File], c)
				}
			}
		} else {
			// Low severity
			if m.config.LowSeverityMerge == "to_summary" {
				res.SummaryAddons = append(res.SummaryAddons, c)
			} else {
				// If not to summary, maybe discard or separate?
				// For now assume to_summary or ignore.
			}
		}
	}

	// Convert fileGroups to MergedFileComment
	for file, cs := range fileGroups {
		// Sort comments by line number
		sort.Slice(cs, func(i, j int) bool {
			return cs[i].Line < cs[j].Line
		})

		marker := fmt.Sprintf("%s%s:%s:%s%s", config.MarkerAIReviewPrefix, config.MarkerTypeFile, file, commit, config.MarkerAIReviewSuffix)

		res.FileComments = append(res.FileComments, MergedFileComment{
			FilePath: file,
			Comments: cs,
			Marker:   marker,
		})
	}

	// Sort FileComments by FilePath
	sort.Slice(res.FileComments, func(i, j int) bool {
		return res.FileComments[i].FilePath < res.FileComments[j].FilePath
	})

	return res
}

func (m *CommentMerger) isHighSeverity(severty string) bool {
	// Construct a temporary comment to check severity
	c := domain.ReviewComment{Severity: severty}
	return c.IsHighSeverity()
}

func (m *CommentMerger) getFileLink(filePath string) string {
	if m.prWebURL == "" || filePath == "" {
		return filePath
	}
	// Format: {PR_WEB_URL}/diff#{FilePath}
	return fmt.Sprintf("[%s](%s/diff#%s)", filePath, m.prWebURL, filePath)
}

func (m *CommentMerger) getLineLink(filePath string, line int) string {
	if m.prWebURL == "" || line <= 0 {
		return strconv.Itoa(line)
	}
	// Format: {PR_WEB_URL}/diff#{FilePath}?t={Line}
	url := fmt.Sprintf("%s/diff#%s?t=%d", m.prWebURL, filePath, line)
	return fmt.Sprintf("[%d](%s)", line, url)
}

// FormatFileComment generates Markdown for a file comment
func (m *CommentMerger) FormatFileComment(fc *MergedFileComment) string {
	var sb strings.Builder
	sb.WriteString(fc.Marker)
	sb.WriteString("\n\n")

	// Determine max severity for icon
	maxSev := domain.CommentSeverityWarning
	for _, c := range fc.Comments {
		if strings.ToUpper(c.Severity) == domain.CommentSeverityCritical {
			maxSev = domain.CommentSeverityCritical
			break
		}
	}

	icon := "⚠️"
	if maxSev == domain.CommentSeverityCritical {
		icon = "🚫"
	}

	fileLink := m.getFileLink(fc.FilePath)
	sb.WriteString(fmt.Sprintf("## %s %s Code Review\n\n", icon, fileLink))
	sb.WriteString("| Line | Severity | Message |\n")
	sb.WriteString("|------|----------|----------|\n")

	for _, c := range fc.Comments {
		sevBadge := c.Severity
		if strings.ToUpper(sevBadge) == "WARNING" {
			sevBadge = "⚠️ WARNING"
		} else if strings.ToUpper(sevBadge) == "CRITICAL" {
			sevBadge = "🚫 CRITICAL"
		}

		// Escape pipes and newlines
		msg := strings.ReplaceAll(c.Comment, "|", "\\|")
		msg = strings.ReplaceAll(msg, "\n", "<br>")

		sb.WriteString(fmt.Sprintf("| %d | %s | %s |\n", c.Line, sevBadge, msg))
	}

	footer := "*This comment was automatically generated by AI Code Review*"
	if fc.ModelName != "" {
		footer = fmt.Sprintf("*Automatically generated by %s*", fc.ModelName)
	}

	sb.WriteString(fmt.Sprintf("\n---\n%s", footer))
	return sb.String()
}

// FormatSummaryAddons generates Markdown table for INFO/NIT comments
func (m *CommentMerger) FormatSummaryAddons(comments []domain.ReviewComment) string {
	if len(comments) == 0 {
		return ""
	}

	var sb strings.Builder
	sb.WriteString("\n### 📋 Suggestions (INFO/NIT)\n\n")
	sb.WriteString("| File | Line | Suggestion |\n")
	sb.WriteString("|------|------|------|\n")

	// Sort by file then line
	sort.Slice(comments, func(i, j int) bool {
		if comments[i].File != comments[j].File {
			return comments[i].File < comments[j].File
		}
		return comments[i].Line < comments[j].Line
	})

	for _, c := range comments {
		msg := strings.ReplaceAll(c.Comment, "|", "\\|")
		msg = strings.ReplaceAll(msg, "\n", "<br>")

		fileLink := m.getFileLink(c.File)
		lineLink := m.getLineLink(c.File, c.Line)

		sb.WriteString(fmt.Sprintf("| %s | %s | %s |\n", fileLink, lineLink, msg))
	}

	return sb.String()
}
