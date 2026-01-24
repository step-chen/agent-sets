package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"pr-review-automation/internal/domain"
	"time"

	_ "modernc.org/sqlite" // Pure Go driver, CGO-free, compatible with CGO_ENABLED=0
)

type SQLiteRepository struct {
	db *sql.DB
}

func NewSQLiteRepository(dsn string) (*SQLiteRepository, error) {
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}

	// Enable WAL mode for better concurrency
	if _, err := db.Exec("PRAGMA journal_mode=WAL;"); err != nil {
		db.Close()
		return nil, fmt.Errorf("enable wal: %w", err)
	}

	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("ping sqlite: %w", err)
	}

	if err := migrate(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}

	return &SQLiteRepository{db: db}, nil
}

func migrate(db *sql.DB) error {
	schema := `
    CREATE TABLE IF NOT EXISTS reviews (
        id          TEXT PRIMARY KEY,
        project_key TEXT NOT NULL,
        repo_slug   TEXT NOT NULL,
        pr_id       TEXT NOT NULL,
        pr_data     TEXT NOT NULL,
        result_data TEXT NOT NULL,
        created_at  DATETIME DEFAULT CURRENT_TIMESTAMP,
        duration_ms INTEGER,
        status      TEXT NOT NULL
    );
    CREATE INDEX IF NOT EXISTS idx_reviews_pr ON reviews(project_key, repo_slug, pr_id);
    CREATE INDEX IF NOT EXISTS idx_reviews_created ON reviews(created_at);
    `
	_, err := db.Exec(schema)
	return err
}

func (r *SQLiteRepository) SaveReview(ctx context.Context, record *ReviewRecord) error {
	prData, err := json.Marshal(record.PullRequest)
	if err != nil {
		return fmt.Errorf("marshal pr: %w", err)
	}

	resultData, err := json.Marshal(record.Result)
	if err != nil {
		return fmt.Errorf("marshal result: %w", err)
	}

	_, err = r.db.ExecContext(ctx, `
        INSERT INTO reviews (id, project_key, repo_slug, pr_id, pr_data, result_data, duration_ms, status, created_at)
        VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
    `, record.ID, record.PullRequest.ProjectKey, record.PullRequest.RepoSlug,
		record.PullRequest.ID, string(prData), string(resultData), record.DurationMs, record.Status, record.CreatedAt)
	return err
}

func (r *SQLiteRepository) GetReview(ctx context.Context, id string) (*ReviewRecord, error) {
	row := r.db.QueryRowContext(ctx, `
        SELECT id, pr_data, result_data, created_at, duration_ms, status
        FROM reviews WHERE id = ?
    `, id)
	return scanReview(row)
}

func (r *SQLiteRepository) ListReviewsByPR(ctx context.Context, projectKey, repoSlug, prID string) ([]*ReviewRecord, error) {
	rows, err := r.db.QueryContext(ctx, `
        SELECT id, pr_data, result_data, created_at, duration_ms, status
        FROM reviews 
        WHERE project_key = ? AND repo_slug = ? AND pr_id = ?
        ORDER BY created_at DESC
    `, projectKey, repoSlug, prID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var reviews []*ReviewRecord
	for rows.Next() {
		record, err := scanReview(rows)
		if err != nil {
			slog.Warn("scan review failed", "error", err)
			continue
		}
		reviews = append(reviews, record)
	}
	return reviews, rows.Err()
}

func (r *SQLiteRepository) ListRecentReviews(ctx context.Context, limit int) ([]*ReviewRecord, error) {
	rows, err := r.db.QueryContext(ctx, `
        SELECT id, pr_data, result_data, created_at, duration_ms, status
        FROM reviews 
        ORDER BY created_at DESC
        LIMIT ?
    `, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var reviews []*ReviewRecord
	for rows.Next() {
		record, err := scanReview(rows)
		if err != nil {
			slog.Warn("scan review failed", "error", err)
			continue
		}
		reviews = append(reviews, record)
	}
	return reviews, rows.Err()
}

func (r *SQLiteRepository) Close() error {
	return r.db.Close()
}

// Scanner interface to support both Row and Rows
type Scanner interface {
	Scan(dest ...any) error
}

func scanReview(s Scanner) (*ReviewRecord, error) {
	var id, prData, resultData, status string
	var createdAt time.Time
	var durationMs int64

	if err := s.Scan(&id, &prData, &resultData, &createdAt, &durationMs, &status); err != nil {
		return nil, err
	}

	var pr domain.PullRequest
	if err := json.Unmarshal([]byte(prData), &pr); err != nil {
		return nil, fmt.Errorf("unmarshal pr: %w", err)
	}

	var result domain.ReviewResult
	if err := json.Unmarshal([]byte(resultData), &result); err != nil {
		return nil, fmt.Errorf("unmarshal result: %w", err)
	}

	return &ReviewRecord{
		ID:          id,
		PullRequest: &pr,
		Result:      &result,
		CreatedAt:   createdAt,
		DurationMs:  durationMs,
		Status:      status,
	}, nil
}
