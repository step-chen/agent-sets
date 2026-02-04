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

	// Verify directly via DB since GetReview was removed (it's write-only audit log)
	var count int
	err = repo.db.QueryRow("SELECT COUNT(*) FROM reviews WHERE id = ?", record.ID).Scan(&count)
	if err != nil {
		t.Fatalf("failed to query db: %v", err)
	}
	if count != 1 {
		t.Errorf("expected 1 record, got %d", count)
	}
}
