package processor

import (
	"context"
	"errors"
	"testing"

	"pr-review-automation/internal/config"
	"pr-review-automation/internal/domain"
	"strings"
)

// MockReviewer mocks the Reviewer interface
type MockReviewer struct {
	ReviewPRFunc func(ctx context.Context, req *domain.ReviewRequest) (*domain.ReviewResult, error)
}

func (m *MockReviewer) ReviewPR(ctx context.Context, req *domain.ReviewRequest) (*domain.ReviewResult, error) {
	if m.ReviewPRFunc != nil {
		return m.ReviewPRFunc(ctx, req)
	}
	return nil, nil // Default
}

// MockCommenter mocks the Commenter interface
type MockCommenter struct {
	CallToolFunc func(ctx context.Context, serverName, toolName string, args map[string]interface{}) (any, error)
}

func (m *MockCommenter) CallTool(ctx context.Context, serverName, toolName string, args map[string]interface{}) (any, error) {
	if m.CallToolFunc != nil {
		return m.CallToolFunc(ctx, serverName, toolName, args)
	}
	// Return a default suitable for parsing (empty bitbucket comments response)
	if toolName == config.ToolBitbucketGetComments {
		return `{"values": []}`, nil
	}
	return nil, nil // Default
}

func TestPRProcessor_ProcessPullRequest_Success(t *testing.T) {
	// Setup mocks
	mockReviewer := &MockReviewer{
		ReviewPRFunc: func(ctx context.Context, req *domain.ReviewRequest) (*domain.ReviewResult, error) {
			return &domain.ReviewResult{
				Comments: []domain.ReviewComment{
					{File: "main.go", Line: 10, Comment: "Fix this"},
				},
				Score:   90,
				Summary: "Good PR",
			}, nil
		},
	}

	callCount := 0
	mockCommenter := &MockCommenter{
		CallToolFunc: func(ctx context.Context, serverName, toolName string, args map[string]interface{}) (any, error) {
			callCount++
			// Helper to simulate comments response
			if toolName == config.ToolBitbucketGetComments {
				return `{"values":[]}`, nil
			}
			if toolName == config.ToolBitbucketGetDiff {
				return `diff --git a/main.go b/main.go
index 123..456 100644
--- a/main.go
+++ b/main.go
@@ -1,1 +1,10 @@
+line 1
+line 2
+line 3
+line 4
+line 5
+line 6
+line 7
+line 8
+line 9
+line 10`, nil
			}
			if toolName == config.ToolBitbucketAddComment {
				// Verify lineNumber is string if present
				if val, ok := args["lineNumber"]; ok {
					if _, okStr := val.(string); !okStr {
						// Create a mock testing.T-like panic or log since we don't have *testing.T here readily available inside the struct unless captured
						// But for this simple mock, we can just panic to fail the test
						panic("lineNumber must be a string")
					}
				}
			}
			return nil, nil
		},
	}

	// Create processor
	p := NewPRProcessor(&config.Config{}, mockReviewer, mockCommenter, nil)

	// Test data
	pr := &domain.PullRequest{
		ID:          "123",
		ProjectKey:  "PROJ",
		RepoSlug:    "repo",
		Title:       "Test PR",
		Description: "Fix bug",
		Author:      "dev",
	}

	// Execute
	err := p.ProcessPullRequest(context.Background(), pr)

	// Verify
	if err != nil {
		t.Errorf("Expected success, got error: %v", err)
	}

	// Expect 3 calls: 1 fetch comments, 1 post comment, 1 post summary
	if callCount != 3 {
		t.Errorf("Expected 3 CallTool invocations, got %d", callCount)
	}
}

func TestPRProcessor_ProcessPullRequest_ReviewFail(t *testing.T) {
	mockReviewer := &MockReviewer{
		ReviewPRFunc: func(ctx context.Context, req *domain.ReviewRequest) (*domain.ReviewResult, error) {
			return nil, errors.New("review failed")
		},
	}
	mockCommenter := &MockCommenter{}

	p := NewPRProcessor(&config.Config{}, mockReviewer, mockCommenter, nil)

	err := p.ProcessPullRequest(context.Background(), &domain.PullRequest{ID: "123"})
	if err == nil {
		t.Error("Expected error, got nil")
	}
}

func TestPRProcessor_ProcessPullRequest_SummaryHeaderCleaning(t *testing.T) {
	// Setup mocks to return a summary with header
	mockReviewer := &MockReviewer{
		ReviewPRFunc: func(ctx context.Context, req *domain.ReviewRequest) (*domain.ReviewResult, error) {
			return &domain.ReviewResult{
				Comments: []domain.ReviewComment{}, // No comments to simplify
				Score:    90,
				Summary:  "# Bad Header\n# Another Header\nNormal text",
			}, nil
		},
	}

	var postedSummary string
	mockCommenter := &MockCommenter{
		CallToolFunc: func(ctx context.Context, serverName, toolName string, args map[string]interface{}) (any, error) {
			if toolName == config.ToolBitbucketGetComments {
				return `{"values":[]}`, nil
			}
			if toolName == config.ToolBitbucketGetDiff {
				return `diff ...`, nil
			}
			if toolName == config.ToolBitbucketAddComment {
				// Check if this is the summary comment (no lineNumber/filePath usually, or specific text)
				if text, ok := args["commentText"].(string); ok {
					if strings.Contains(text, "AI Review Summary") {
						postedSummary = text
					}
				}
			}
			return nil, nil
		},
	}

	// Enable comment merge to trigger summary posting
	cfg := &config.Config{
		Pipeline: config.PipelineConfig{
			CommentMerge: config.CommentMergeConfig{
				Enabled: true,
			},
		},
	}
	p := NewPRProcessor(cfg, mockReviewer, mockCommenter, nil)
	pr := &domain.PullRequest{ID: "123", ProjectKey: "PROJ", RepoSlug: "repo"}

	p.ProcessPullRequest(context.Background(), pr)

	if strings.Contains(postedSummary, "# Bad Header") {
		t.Errorf("Summary should not contain headers. Got: %s", postedSummary)
	}
	if strings.Contains(postedSummary, "**Bad Header**") {
		t.Errorf("Summary should NOT contain bolded text. Got: %s", postedSummary)
	}
	if !strings.Contains(postedSummary, "Bad Header") {
		t.Errorf("Summary should contain plain text. Got: %s", postedSummary)
	}
}
