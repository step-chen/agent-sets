package pipeline

import (
	"testing"
)

func TestParseReviewResult_NormalComments(t *testing.T) {
	// Normal JSON
	jsonStr := `{
		"comments": [{"path": "bar.go", "line": 20, "message": "nit", "severity": "NIT"}],
		"score": 90,
		"summary": "Good"
	}`

	result, err := parseReviewResult(jsonStr)
	if err != nil {
		t.Fatalf("Failed to parse: %v", err)
	}

	if len(result.Comments) != 1 {
		t.Fatalf("Expected 1 comment, got %d", len(result.Comments))
	}
}

func TestParseReviewResult_EmptyComments(t *testing.T) {
	// Empty comments
	jsonStr := `{
        "comments": [],
        "score": 100,
        "summary": "Excellent"
    }`

	result, err := parseReviewResult(jsonStr)
	if err != nil {
		t.Fatalf("Failed to parse: %v", err)
	}

	if len(result.Comments) != 0 {
		t.Fatalf("Expected 0 comments, got %d", len(result.Comments))
	}
}
