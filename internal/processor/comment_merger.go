package processor

import (
	"bytes"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"pr-review-automation/internal/config"
	"pr-review-automation/internal/domain"
)

// CommentMerger handles comment grouping and merging
type CommentMerger struct {
	config         *config.CommentMergeConfig
	prWebURL       string
	templates      *CommentTemplates
	showConfidence bool
}

// NewCommentMerger creates a new CommentMerger
func NewCommentMerger(cfg *config.CommentMergeConfig, prWebURL string, templates *CommentTemplates, showConfidence bool) *CommentMerger {
	return &CommentMerger{config: cfg, prWebURL: prWebURL, templates: templates, showConfidence: showConfidence}
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
	Commit    string // For marker generation
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

	// Step 1: Deduplicate by location (File:Line)
	comments = m.deduplicateByLocation(comments)

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

		res.FileComments = append(res.FileComments, MergedFileComment{
			FilePath: file,
			Comments: cs,
			Commit:   commit,
		})
	}

	// Sort FileComments by FilePath
	sort.Slice(res.FileComments, func(i, j int) bool {
		return res.FileComments[i].FilePath < res.FileComments[j].FilePath
	})

	return res
}

// deduplicateByLocation merges comments for the same location
func (m *CommentMerger) deduplicateByLocation(comments []domain.ReviewComment) []domain.ReviewComment {
	groups := make(map[string][]domain.ReviewComment)
	var keys []string

	for _, c := range comments {
		// Use Line as anchor
		key := fmt.Sprintf("%s:%d", c.File, c.Line)
		if _, exists := groups[key]; !exists {
			keys = append(keys, key)
		}
		groups[key] = append(groups[key], c)
	}

	var result []domain.ReviewComment
	for _, key := range keys {
		result = append(result, m.mergeGroup(groups[key]))
	}
	return result
}

// mergeGroup merges a list of comments for the same location into a single comment
func (m *CommentMerger) mergeGroup(group []domain.ReviewComment) domain.ReviewComment {
	if len(group) == 1 {
		return group[0]
	}

	// Sort group by length (descending) to prefer richer comments as base
	sort.Slice(group, func(i, j int) bool {
		return len(group[i].Comment) > len(group[j].Comment)
	})

	base := group[0]
	for i := 1; i < len(group); i++ {
		if !m.isSimilar(base.Comment, group[i].Comment) {
			base.Comment += fmt.Sprintf("\n\n[Also]: %s", group[i].Comment)
		}
		// Upgrade severity if higher
		if severityRank(group[i].Severity) > severityRank(base.Severity) {
			base.Severity = group[i].Severity
		}
		// Keep highest confidence
		if group[i].Confidence > base.Confidence {
			base.Confidence = group[i].Confidence
		}
	}
	return base
}

// isSimilar checks if two strings are roughly the same
func (m *CommentMerger) isSimilar(a, b string) bool {
	aLower := strings.ToLower(strings.TrimSpace(a))
	bLower := strings.ToLower(strings.TrimSpace(b))
	return strings.Contains(aLower, bLower) || strings.Contains(bLower, aLower)
}

func severityRank(s string) int {
	switch s {
	case domain.CommentSeverityCritical:
		return 3
	case domain.CommentSeverityWarning:
		return 2
	case domain.CommentSeverityInfo:
		return 1
	default:
		return 0
	}
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

func (m *CommentMerger) getLocationLink(filePath string, line int) string {
	if m.prWebURL == "" {
		return fmt.Sprintf("%s:%d", filePath, line)
	}
	// Format: [{FilePath}:{Line}]({PR_WEB_URL}/diff#{FilePath}?t={Line})
	url := fmt.Sprintf("%s/diff#%s?t=%d", m.prWebURL, filePath, line)
	return fmt.Sprintf("[%s:%d](%s)", filePath, line, url)
}

// FormatFileComment generates Markdown for a file comment
func (m *CommentMerger) FormatFileComment(fc *MergedFileComment) string {
	if m.templates == nil || m.templates.Inline == nil {
		return ""
	}

	// Prepare rows
	rows := make([][]string, 0, len(fc.Comments))
	maxSev := domain.CommentSeverityWarning

	for _, c := range fc.Comments {
		if strings.ToUpper(c.Severity) == domain.CommentSeverityCritical {
			maxSev = domain.CommentSeverityCritical
		}

		sevBadge := c.Severity
		if strings.ToUpper(sevBadge) == "WARNING" {
			sevBadge = "⚠️ WARNING"
		} else if strings.ToUpper(sevBadge) == "CRITICAL" {
			sevBadge = "🚫 CRITICAL"
		}

		// Escape pipes and newlines
		msg := strings.ReplaceAll(c.Comment, "|", "\\|")
		msg = strings.ReplaceAll(msg, "\n", "<br>")
		msg = strings.ReplaceAll(msg, "\n", "<br>")
		// Confidence is now handled in the table row itself (for file comments, we keep it here for now or move to verify?)
		// Actually, for file comments (inline table), the confidence should still remain in the message or separate column?
		// The requirements didn't specify changing the FileComment table, only the Summary Table and Individual Footer.
		// So I will keep this logic AS IS for FormatFileComment to avoid regression, unless specified.
		// Wait, user said "Individual Comment Footer". FormatFileComment generates a TABLE for multiple comments on one file.
		// So checking if I should touch this. User request: "Individual Comment Footer".
		// FormatFileComment uses `inline.tmpl` which uses `table.tmpl`.
		// It renders a table.
		// Let's keep it as is for now, but I must compile.
		if m.showConfidence {
			msg += fmt.Sprintf(" *(Confidence: %.0f%%)*", c.Confidence*100)
		}

		rows = append(rows, []string{
			strconv.Itoa(int(c.Line)),
			sevBadge,
			msg,
		})
	}

	icon := "⚠️"
	if maxSev == domain.CommentSeverityCritical {
		icon = "🚫"
	}

	data := InlineTemplateData{
		MarkerData: MarkerData{
			Type:   config.MarkerTypeFile,
			File:   fc.FilePath,
			Commit: fc.Commit,
		},
		FooterData: FooterData{
			Model:          fc.ModelName,
			Confidence:     0, // Will be handled per row in the table, or max confidence for the file?
			ShowConfidence: false,
		},
		Icon:  icon,
		Title: m.getFileLink(fc.FilePath) + " Code Review",
		TableData: &TableData{
			Headers: []string{"Line", "Severity", "Message"},
			Rows:    rows,
		},
	}

	var buf bytes.Buffer
	if err := m.templates.Inline.Execute(&buf, data); err != nil {
		return ""
	}
	return buf.String()
}

// FormatSummaryAddons generates Markdown table for INFO/NIT comments
func (m *CommentMerger) FormatSummaryAddons(comments []domain.ReviewComment) string {
	if len(comments) == 0 {
		return ""
	}
	if m.templates == nil || m.templates.Addons == nil {
		return ""
	}

	// Sort by file then line
	sort.Slice(comments, func(i, j int) bool {
		if comments[i].File != comments[j].File {
			return comments[i].File < comments[j].File
		}
		return comments[i].Line < comments[j].Line
	})

	var headers []string
	if m.showConfidence {
		headers = []string{"Location", "Conf", "Suggestion"}
	} else {
		headers = []string{"Location", "Suggestion"}
	}

	rows := make([][]string, 0, len(comments))
	for _, c := range comments {
		msg := strings.ReplaceAll(c.Comment, "|", "\\|")
		msg = strings.ReplaceAll(msg, "\n", "<br>")

		locationLink := m.getLocationLink(c.File, int(c.Line))

		if m.showConfidence {
			confStr := fmt.Sprintf("%.0f%%", c.Confidence*100)
			rows = append(rows, []string{locationLink, confStr, msg})
		} else {
			rows = append(rows, []string{locationLink, msg})
		}
	}

	data := AddonsTemplateData{
		TableData: TableData{
			Headers: headers,
			Rows:    rows,
		},
		ShowConfidence: m.showConfidence,
	}

	var buf bytes.Buffer
	if err := m.templates.Addons.Execute(&buf, data); err != nil {
		return ""
	}
	return buf.String()
}
