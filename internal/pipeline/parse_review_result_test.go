package pipeline

import (
	"testing"
)

func TestParseReviewResult_StringifiedComments(t *testing.T) {
	// The problematic JSON where "comments" is a stringified JSON array
	jsonStr := `{
		"comments": "[{\"path\": \"foo.go\", \"line\": 10, \"message\": \"bug\", \"severity\": \"CRITICAL\"}]",
		"score": 85,
		"summary": "Some summary"
	}`

	result, err := parseReviewResult(jsonStr)
	if err != nil {
		t.Fatalf("Failed to parse: %v", err)
	}

	if len(result.Comments) != 1 {
		t.Fatalf("Expected 1 comment, got %d", len(result.Comments))
	}

	c := result.Comments[0]
	if c.File != "foo.go" {
		t.Errorf("Expected file foo.go, got %s", c.File)
	}
	if c.Severity != "CRITICAL" {
		t.Errorf("Expected severity CRITICAL, got %s", c.Severity)
	}
}

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

func TestParseReviewResult_SingleObjectComment(t *testing.T) {
	// Single object instead of array
	jsonStr := `{
		"comments": {"path": "single.go", "line": 5, "message": "forgot array", "severity": "INFO"},
		"score": 80,
		"summary": "Single object"
	}`

	result, err := parseReviewResult(jsonStr)
	if err != nil {
		t.Fatalf("Failed to parse: %v", err)
	}

	if len(result.Comments) != 1 {
		t.Fatalf("Expected 1 comment, got %d", len(result.Comments))
	}
	if result.Comments[0].File != "single.go" {
		t.Errorf("Expected single.go, got %s", result.Comments[0].File)
	}
}
