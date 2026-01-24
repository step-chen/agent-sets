package storage

import (
	"context"
	"pr-review-automation/internal/domain"
	"time"
)

// ReviewRecord Review persistence record
type ReviewRecord struct {
	ID          string               `json:"id"`
	PullRequest *domain.PullRequest  `json:"pull_request"`
	Result      *domain.ReviewResult `json:"result"`
	CreatedAt   time.Time            `json:"created_at"`
	DurationMs  int64                `json:"duration_ms"`
	Status      string               `json:"status"` // success, error
}

// Repository Storage interface
type Repository interface {
	SaveReview(ctx context.Context, record *ReviewRecord) error
	GetReview(ctx context.Context, id string) (*ReviewRecord, error)
	ListReviewsByPR(ctx context.Context, projectKey, repoSlug, prID string) ([]*ReviewRecord, error)
	ListRecentReviews(ctx context.Context, limit int) ([]*ReviewRecord, error)
	Close() error
}
