package processor

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"pr-review-automation/internal/config"
	"pr-review-automation/internal/domain"
	"pr-review-automation/internal/syncutil"
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
	if toolName == "bitbucket_get_pull_request_comments" {
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
					{File: "main.go", Line: 10, Comment: "Fix this", Confidence: 1.0},
				},
				Score:   90,
				Summary: "Good PR",
			}, nil
		},
	}

	callCount := atomic.Int32{}
	mockCommenter := &MockCommenter{
		CallToolFunc: func(ctx context.Context, serverName, toolName string, args map[string]interface{}) (any, error) {
			callCount.Add(1)
			// Helper to simulate comments response
			if toolName == "bitbucket_get_pull_request_comments" {
				return `{"values":[]}`, nil
			}
			if toolName == "bitbucket_get_pull_request_diff" {
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
			if toolName == "bitbucket_add_pull_request_comment" {
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
	tracker := syncutil.NewTracker()
	cfg := &config.Config{
		Prompts: config.PromptsConfig{
			Dir: "/home/stephen/workspace/agent-sets/prompts",
		},
		MCP: config.MCPConfig{
			Bitbucket: config.MCPServerConfig{
				Tools: map[string]string{
					config.ToolKeyGetComments: "bitbucket_get_pull_request_comments",
					config.ToolKeyGetDiff:     "bitbucket_get_pull_request_diff",
					config.ToolKeyAddComment:  "bitbucket_add_pull_request_comment",
				},
			},
		},
	}
	p, err := NewPRProcessor(cfg, mockReviewer, mockCommenter, nil, tracker)
	if err != nil {
		t.Fatalf("NewPRProcessor failed: %v", err)
	}

	// Test data
	pr := &domain.PullRequest{
		ID:          "123",
		ProjectKey:  "PROJ",
		RepoSlug:    "repo",
		Title:       "Test PR",
		Description: "Fix bug",
		Author:      "dev",
	}

	// Test execution
	err = p.ProcessPullRequest(context.Background(), &domain.ReviewRequest{PR: pr})

	// Verify
	if err != nil {
		t.Errorf("Expected success, got error: %v", err)
	}

	// Expect 4 calls: 1 fetch diff, 1 fetch comments, 1 post comment, 1 post summary
	if val := callCount.Load(); val != 4 {
		t.Errorf("Expected 4 CallTool invocations, got %d", val)
	}
}

func TestPRProcessor_ProcessPullRequest_ReviewFail(t *testing.T) {
	mockReviewer := &MockReviewer{
		ReviewPRFunc: func(ctx context.Context, req *domain.ReviewRequest) (*domain.ReviewResult, error) {
			return nil, errors.New("review failed")
		},
	}
	mockCommenter := &MockCommenter{}

	tracker := syncutil.NewTracker()
	cfg := &config.Config{
		Prompts: config.PromptsConfig{
			Dir: "/home/stephen/workspace/agent-sets/prompts",
		},
	}
	p, err := NewPRProcessor(cfg, mockReviewer, mockCommenter, nil, tracker)
	if err != nil {
		t.Fatalf("NewPRProcessor failed: %v", err)
	}

	err = p.ProcessPullRequest(context.Background(), &domain.ReviewRequest{PR: &domain.PullRequest{ID: "123"}})
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
			if toolName == "bitbucket_get_pull_request_comments" {
				return `{"values":[]}`, nil
			}
			if toolName == "bitbucket_get_pull_request_diff" {
				return `diff ...`, nil
			}
			if toolName == "bitbucket_add_pull_request_comment" {
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
		Prompts: config.PromptsConfig{
			Dir: "/home/stephen/workspace/agent-sets/prompts",
		},
		MCP: config.MCPConfig{
			Bitbucket: config.MCPServerConfig{
				Tools: map[string]string{
					config.ToolKeyGetComments: "bitbucket_get_pull_request_comments",
					config.ToolKeyGetDiff:     "bitbucket_get_pull_request_diff",
					config.ToolKeyAddComment:  "bitbucket_add_pull_request_comment",
				},
			},
		},
	}
	tracker := syncutil.NewTracker()
	p, err := NewPRProcessor(cfg, mockReviewer, mockCommenter, nil, tracker)
	if err != nil {
		t.Fatalf("NewPRProcessor failed: %v", err)
	}
	pr := &domain.PullRequest{ID: "123", ProjectKey: "PROJ", RepoSlug: "repo"}

	p.ProcessPullRequest(context.Background(), &domain.ReviewRequest{PR: pr})

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

func TestPRProcessor_IndividualComment_Format(t *testing.T) {
	// Setup mocks
	mockReviewer := &MockReviewer{
		ReviewPRFunc: func(ctx context.Context, req *domain.ReviewRequest) (*domain.ReviewResult, error) {
			return &domain.ReviewResult{
				Comments: []domain.ReviewComment{
					{File: "main.go", Line: 10, Comment: "Fix this", Confidence: 1.0},
				},
				Score:   90,
				Summary: "Good PR",
				Model:   "test-model",
			}, nil
		},
	}

	var postedComment string
	mockCommenter := &MockCommenter{
		CallToolFunc: func(ctx context.Context, serverName, toolName string, args map[string]interface{}) (any, error) {
			if toolName == "bitbucket_get_pull_request_comments" {
				return `{"values":[]}`, nil
			}
			if toolName == "bitbucket_get_pull_request_diff" {
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
			if toolName == "bitbucket_add_pull_request_comment" {
				// Capture the comment text
				if text, ok := args["commentText"].(string); ok {
					postedComment = text
				}
			}
			return nil, nil
		},
	}

	// Disable comment merge to enforce individual comments
	cfg := &config.Config{
		Pipeline: config.PipelineConfig{
			CommentMerge: config.CommentMergeConfig{
				Enabled: false,
			},
		},
		Prompts: config.PromptsConfig{
			Dir: "/home/stephen/workspace/agent-sets/prompts",
		},
		MCP: config.MCPConfig{
			Bitbucket: config.MCPServerConfig{
				Tools: map[string]string{
					config.ToolKeyGetComments: "bitbucket_get_pull_request_comments",
					config.ToolKeyGetDiff:     "bitbucket_get_pull_request_diff",
					config.ToolKeyAddComment:  "bitbucket_add_pull_request_comment",
				},
			},
		},
	}
	tracker := syncutil.NewTracker()
	p, err := NewPRProcessor(cfg, mockReviewer, mockCommenter, nil, tracker)
	if err != nil {
		t.Fatalf("NewPRProcessor failed: %v", err)
	}
	pr := &domain.PullRequest{ID: "123", ProjectKey: "PROJ", RepoSlug: "repo", LatestCommit: "commit123"}

	p.ProcessPullRequest(context.Background(), &domain.ReviewRequest{PR: pr})

	// Expected format: <!-- ai-review::main.go:10:commit123-->\n\nFix this...
	expectedMarkerSuffix := config.MarkerAIReviewSuffix + "\n\n"
	if !strings.Contains(postedComment, expectedMarkerSuffix) {
		t.Errorf("Comment text missing double newline after marker.\nGot:\n%q\nExpected to contain:\n%q", postedComment, expectedMarkerSuffix)
	}

	expectedFooter := "*test-model*"
	if !strings.Contains(postedComment, expectedFooter) {
		t.Errorf("Comment text missing model footer.\nGot:\n%q", postedComment)
	}
}
