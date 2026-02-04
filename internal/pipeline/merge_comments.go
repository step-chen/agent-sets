package pipeline

import (
	"fmt"
	"sort"
	"strings"

	"pr-review-automation/internal/config"
	"pr-review-automation/internal/domain"
)

// CommentMerger handles the logic for deduplicating and merging comments
type CommentMerger struct {
	cfg config.CommentMergeConfig
}

// NewCommentMerger creates a new CommentMerger
func NewCommentMerger(cfg config.CommentMergeConfig) *CommentMerger {
	return &CommentMerger{cfg: cfg}
}

// Merge deduplicates comments based on location (File:Line) and optionally moves low-severity ones to summary.
func (m *CommentMerger) Merge(comments []domain.ReviewComment) ([]domain.ReviewComment, string) {
	if !m.cfg.Enabled {
		return comments, ""
	}

	// Group by "File:Line"
	// Key format: "path/to/file.go:42"
	groupByLocation := make(map[string][]domain.ReviewComment)

	// Keep track of order to maintain stability
	var locationKeys []string
	keySeen := make(map[string]bool)

	for _, c := range comments {
		// Calculate Location Key
		// Note: We use the Line as an achor.
		lineVal := int(c.Line)
		key := fmt.Sprintf("%s:%d", c.File, lineVal)

		if _, exists := groupByLocation[key]; !exists {
			groupByLocation[key] = []domain.ReviewComment{}
			if !keySeen[key] {
				locationKeys = append(locationKeys, key)
				keySeen[key] = true
			}
		}
		groupByLocation[key] = append(groupByLocation[key], c)
	}

	var finalComments []domain.ReviewComment
	var lowSeverityLines []string

	// Process each group
	for _, key := range locationKeys {
		group := groupByLocation[key]
		if len(group) == 0 {
			continue
		}

		mergedComment := m.mergeGroup(group)

		// Check if it should be moved to summary
		// Strategy: If "to_summary" is enabled, only move if it's NOT High Severity
		if m.cfg.LowSeverityMerge == "to_summary" && !mergedComment.IsHighSeverity() {
			lowSeverityLines = append(lowSeverityLines,
				fmt.Sprintf("- **[`%s:%d`](%s#L%d)**: %s",
					mergedComment.File, int(mergedComment.Line),
					mergedComment.File, int(mergedComment.Line),
					mergedComment.Comment))
		} else {
			finalComments = append(finalComments, mergedComment)
		}
	}

	var appendix string
	if len(lowSeverityLines) > 0 {
		appendix = "\n\n### 📋 Suggestions (INFO/NIT)\n\n" + strings.Join(lowSeverityLines, "\n")
	}

	return finalComments, appendix
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
	combinedMsg := base.Comment

	// Simple heuristic: If multiple comments exist for the same line,
	// checking if they are semantically duplicates is hard without LLM.
	// But V5 Strategy is: "Force Aggregation".
	// We will append others if they are not significantly similar.

	for i := 1; i < len(group); i++ {
		other := group[i]
		if isSimilar(base.Comment, other.Comment) {
			continue // Skip duplicate
		}
		// Append distinct issue
		combinedMsg += fmt.Sprintf("\n\n[Also]: %s", other.Comment)
	}

	// Upgrade severity if any in group is higher
	highestSeverity := base.Severity
	for _, c := range group {
		if severityRank(c.Severity) > severityRank(highestSeverity) {
			highestSeverity = c.Severity
		}
	}

	return domain.ReviewComment{
		File:     base.File,
		Line:     base.Line,
		Comment:  combinedMsg,
		Severity: highestSeverity,
	}
}

// isSimilar checks if two strings are roughly the same (simple containment or prefix/suffix)
func isSimilar(a, b string) bool {
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
	case domain.CommentSeverityNit:
		return 0
	default:
		return 0
	}
}
