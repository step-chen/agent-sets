package storage

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"pr-review-automation/internal/agent"
	"pr-review-automation/internal/domain"
)

func TestSQLiteRepository(t *testing.T) {
	// Create temp dir for db
	tmpDir, err := os.MkdirTemp("", "pr-review-storage-test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	dbPath := filepath.Join(tmpDir, "test.db")

	repo, err := NewSQLiteRepository(dbPath)
	if err != nil {
		t.Fatalf("failed to create repository: %v", err)
	}
	defer repo.Close()

	// Prepare data
	pr := &domain.PullRequest{
		ID:          "101",
		ProjectKey:  "TEST",
		RepoSlug:    "repo-1",
		Title:       "Test PR",
		Description: "A test PR",
		Author:      "tester",
	}

	result := &agent.ReviewResult{
		Score:   88,
		Summary: "Looks good",
		Comments: []agent.ReviewComment{
			{File: "main.go", Line: 10, Comment: "Nice"},
		},
		Model: "gemini-test",
	}

	record := &ReviewRecord{
		ID:          "test-record-1",
		PullRequest: pr,
		Result:      result,
		CreatedAt:   time.Now().UTC(),
		DurationMs:  1500,
		Status:      "success",
	}

	// Test Save
	ctx := context.Background()
	if err := repo.SaveReview(ctx, record); err != nil {
		t.Fatalf("SaveReview failed: %v", err)
	}

	// Test Get
	saved, err := repo.GetReview(ctx, record.ID)
	if err != nil {
		t.Fatalf("GetReview failed: %v", err)
	}

	if saved.ID != record.ID {
		t.Errorf("expected ID %s, got %s", record.ID, saved.ID)
	}
	if saved.PullRequest.ID != pr.ID {
		t.Errorf("expected PR ID %s, got %s", pr.ID, saved.PullRequest.ID)
	}
	if saved.Result.Score != result.Score {
		t.Errorf("expected score %d, got %d", result.Score, saved.Result.Score)
	}

	// Test List by PR
	list, err := repo.ListReviewsByPR(ctx, "TEST", "repo-1", "101")
	if err != nil {
		t.Fatalf("ListReviewsByPR failed: %v", err)
	}
	if len(list) != 1 {
		t.Errorf("expected 1 record, got %d", len(list))
	}

	// Test Recent
	recent, err := repo.ListRecentReviews(ctx, 10)
	if err != nil {
		t.Fatalf("ListRecentReviews failed: %v", err)
	}
	if len(recent) != 1 {
		t.Errorf("expected 1 record, got %d", len(recent))
	}
}
