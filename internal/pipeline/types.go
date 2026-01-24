package pipeline

import (
	"context"

	"pr-review-automation/internal/client"
	"pr-review-automation/internal/config"
	"pr-review-automation/internal/domain"
	"pr-review-automation/internal/llm"
)

// LLMClient alias to internal llm client
type LLMClient = llm.Client

// Pipeline executes the 3-stage PR review process
type Pipeline struct {
	cfg       *config.Config
	mcpClient *client.MCPClient
	llmClient LLMClient

	stage1 Stage1DiffExtractor
	stage2 Stage2ContextCollector
	stage3 Stage3Reviewer
}

// ReviewRequest represents the input for the pipeline
type ReviewRequest struct {
	PR           domain.PullRequest
	LatestCommit string
}

// FileChange represents a file change from Stage 1
type FileChange struct {
	Path       string   // Full file path
	ChangeType string   // add, modify, delete, rename
	OldPath    string   // Old path if renamed
	HunkLines  []string // Simplified diff content
}

// FileContent represents file context from Stage 2
type FileContent struct {
	Path      string
	Content   string
	IsDiffed  bool   // true if this file was in the diff
	Relevance string // direct, import, test, config
}

// Stage1DiffExtractor defines the interface for Stage 1
type Stage1DiffExtractor interface {
	ExtractDiffs(ctx context.Context, req ReviewRequest) ([]FileChange, error)
}

// Stage2ContextCollector defines the interface for Stage 2
type Stage2ContextCollector interface {
	CollectContext(ctx context.Context, req ReviewRequest, changes []FileChange) ([]FileContent, error)
}

// Stage3Reviewer defines the interface for Stage 3
type Stage3Reviewer interface {
	Review(ctx context.Context, req ReviewRequest, changes []FileChange, context []FileContent) (*domain.ReviewResult, error)
}
