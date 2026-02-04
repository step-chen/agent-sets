package pipeline

import (
	"strings"
	"testing"

	"pr-review-automation/internal/config"
	"pr-review-automation/internal/domain"
)

func TestMergeComments_LocationAggregation(t *testing.T) {
	cfg := config.CommentMergeConfig{
		Enabled:          true,
		LowSeverityMerge: "none",
	}
	merger := NewCommentMerger(cfg)

	comments := []domain.ReviewComment{
		{File: "main.go", Line: 10, Comment: "Error handling missing", Severity: "CRITICAL"},
		{File: "main.go", Line: 10, Comment: "Also check error variable", Severity: "INFO"}, // Similar (partially)
		{File: "main.go", Line: 10, Comment: "Variable naming violation", Severity: "NIT"},  // Distinct
		{File: "utils.go", Line: 5, Comment: "Good job", Severity: "INFO"},
	}

	merged, _ := merger.Merge(comments)

	if len(merged) != 2 {
		t.Errorf("Expected 2 comments (1 for main.go:10, 1 for utils.go:5), got %d", len(merged))
	}

	// Verify main.go:10 aggregation
	var mainComment domain.ReviewComment
	for _, c := range merged {
		if c.File == "main.go" {
			mainComment = c
			break
		}
	}

	if mainComment.Severity != "CRITICAL" {
		t.Errorf("Expected severity upgrade to CRITICAL, got %s", mainComment.Severity)
	}

	if !strings.Contains(mainComment.Comment, "Error handling missing") {
		t.Error("Missing base comment")
	}
	if !strings.Contains(mainComment.Comment, "Variable naming violation") {
		t.Error("Missing distinct comment")
	}
	// "Also check error variable" contains "check error" vs "Error handling" -> might not match "isSimilar" simple check
	// Let's check logic: "Error handling missing" vs "Also check error variable"
	// strings.Contains("error handling missing", "also check error variable") -> false
	// strings.Contains("also check error variable", "error handling missing") -> false
	// So it should be appended.
	if !strings.Contains(mainComment.Comment, "Also check error variable") {
		t.Error("Missing similar-but-distinct comment")
	}
}

func TestMergeComments_ToSummary(t *testing.T) {
	cfg := config.CommentMergeConfig{
		Enabled:          true,
		LowSeverityMerge: "to_summary",
	}
	merger := NewCommentMerger(cfg)

	comments := []domain.ReviewComment{
		{File: "main.go", Line: 10, Comment: "Critical bug", Severity: "CRITICAL"},
		{File: "utils.go", Line: 5, Comment: "Fix typo", Severity: "NIT"},
		{File: "utils.go", Line: 5, Comment: "Another typo", Severity: "INFO"},
	}

	merged, appendix := merger.Merge(comments)

	if len(merged) != 1 {
		t.Errorf("Expected 1 critical comment, got %d", len(merged))
	}
	if merged[0].File != "main.go" {
		t.Errorf("Expected main.go comment to be preserved")
	}

	if !strings.Contains(appendix, "Fix typo") {
		t.Error("Appendix missing 'Fix typo'")
	}
	if !strings.Contains(appendix, "Another typo") {
		t.Error("Appendix missing 'Another typo'")
	}
}

func TestMergeComments_ExactDuplicate(t *testing.T) {
	cfg := config.CommentMergeConfig{
		Enabled: true,
	}
	merger := NewCommentMerger(cfg)

	comments := []domain.ReviewComment{
		{File: "main.go", Line: 10, Comment: "Fix typo", Severity: "NIT"},
		{File: "main.go", Line: 10, Comment: "Fix typo", Severity: "NIT"},
	}

	merged, _ := merger.Merge(comments)

	if len(merged) != 1 {
		t.Errorf("Expected 1 comment, got %d", len(merged))
	}
	if strings.Contains(merged[0].Comment, "[Also]") {
		t.Error("Should not have appended duplicate comment")
	}
}
