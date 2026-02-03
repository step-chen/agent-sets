package pipeline

import (
	"context"
	"testing"

	"pr-review-automation/internal/config"
	"pr-review-automation/internal/domain"
)

func TestDegradationManager_ApplyStrategy_DegradeHints(t *testing.T) {
	// Setup
	cfg := config.DegradationConfig{
		L1ContextLines: 10,
		L3DiffOnly:     true,
	}
	dm := NewDegradationManager(cfg, 500, nil)

	changes := []FileChange{{Path: "main.go", HunkLines: []string{"+diff"}}}
	contextFiles := []FileContent{
		{Path: "main.go", Content: "line1\nline2\nline3\nline4\nline5\nline6\nline7\nline8\nline9\nline10\nline11\nline12\n..."},
	}

	// Mock ReviewFunc
	var receivedContext []FileContent
	reviewFunc := func(ctx context.Context, req ReviewRequest, changes []FileChange, contextFiles []FileContent) (*domain.ReviewResult, error) {
		receivedContext = contextFiles
		return &domain.ReviewResult{}, nil
	}

	// Test Case 1: Hint 0 (Normal) - Expect Full Context (since tokens < max)
	t.Run("Hint 0 - Full Context", func(t *testing.T) {
		req := ReviewRequest{DegradeHint: 0}
		_, err := dm.ApplyStrategy(context.Background(), req, changes, contextFiles, "tmpl", "prompt", reviewFunc)
		if err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}
		if len(receivedContext) != 1 {
			t.Errorf("Expected 1 context file, got %d", len(receivedContext))
		}
		if len(receivedContext[0].Content) < 20 { // Simple check it wasn't truncated significantly
			t.Errorf("Expected full context, got truncated: %s", receivedContext[0].Content)
		}
	})

	// Test Case 2: Hint 1 (L1) - Expect Truncated Context
	t.Run("Hint 1 - L1 Truncation", func(t *testing.T) {
		req := ReviewRequest{DegradeHint: 1}
		// With L1ContextLines=10, limit=20. Content has >20 lines?
		// My content string above is short. Let's make it LONG.
		longContext := make([]FileContent, 1)
		longContent := ""
		for i := 0; i < 200; i++ {
			longContent += "line\n"
		}
		longContext[0] = FileContent{Path: "long.go", Content: longContent}

		_, err := dm.ApplyStrategy(context.Background(), req, changes, longContext, "tmpl", "prompt", reviewFunc)
		if err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}
		// Expect truncation
		// L1 limit is heuristic: cfg.L1ContextLines * 2. 10*2 = 20.
		// Content has 100 lines.
		if len(receivedContext) != 1 {
			t.Fatalf("Expected 1 context file")
		}
		if len(receivedContext[0].Content) >= len(longContent) {
			t.Errorf("Expected context to be truncated. Original: %d, Got: %d", len(longContent), len(receivedContext[0].Content))
			t.Logf("Got content: %s", receivedContext[0].Content)
		}
	})

	// Test Case 3: Hint 2 (L3) - Expect No Context
	t.Run("Hint 2 - L3 Diff Only", func(t *testing.T) {
		req := ReviewRequest{DegradeHint: 2}
		_, err := dm.ApplyStrategy(context.Background(), req, changes, contextFiles, "tmpl", "prompt", reviewFunc)
		if err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}
		if len(receivedContext) != 0 {
			t.Errorf("Expected 0 context files (Diff Only), got %d", len(receivedContext))
		}
	})
}
