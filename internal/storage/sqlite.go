package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	_ "github.com/mattn/go-sqlite3" // CGO driver, high performance
)

type SQLiteRepository struct {
	db *sql.DB
}

func NewSQLiteRepository(dsn string) (*SQLiteRepository, error) {
	db, err := sql.Open("sqlite3", dsn)
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

func (r *SQLiteRepository) Close() error {
	return r.db.Close()
}
