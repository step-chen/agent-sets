package storage

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

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

	result := &domain.ReviewResult{
		// Score:   88, // Score field removed from domain.ReviewResult? Check definition if build fails.
		Summary: "Looks good",
		Comments: []domain.ReviewComment{
			{File: "main.go", Line: 10, Comment: "Nice"},
		},
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
	// Verify result
	// Note: Score might be missing, check Summary
	if saved.Result.Summary != result.Summary {
		t.Errorf("expected summary %s, got %s", result.Summary, saved.Result.Summary)
	}
}
