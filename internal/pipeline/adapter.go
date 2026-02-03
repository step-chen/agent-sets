package pipeline

import (
	"context"
	"fmt"
	"log/slog"

	"pr-review-automation/internal/client"
	"pr-review-automation/internal/config"
	codecontext "pr-review-automation/internal/context"
	"pr-review-automation/internal/domain"
)

// PipelineAdapter adapts the Pipeline to the Reviewer interface
type PipelineAdapter struct {
	pipeline *Pipeline
}

// NewPipelineAdapter creates a new adapter for the pipeline
func NewPipelineAdapter(cfg *config.Config, mcpClient *client.MCPClient, llm LLMClient, promptLoader *PromptLoader) *PipelineAdapter {
	p := &Pipeline{
		cfg:       cfg,
		mcpClient: mcpClient,
		llmClient: llm,
	}

	// Initialize Context Engine
	ctxEngine := codecontext.NewContextEngine()

	// Initialize FileCache
	fileCache := NewFileCache()

	// Initialize stages
	p.stage1 = NewStage1(&cfg.Pipeline, mcpClient, llm, promptLoader, fileCache)
	p.stage2 = NewStage2(&cfg.Pipeline, mcpClient, llm, promptLoader, ctxEngine, fileCache)
	p.stage3 = NewStage3(&cfg.Pipeline, mcpClient, llm, promptLoader)

	return &PipelineAdapter{
		pipeline: p,
	}
}

// ReviewPR implements the Reviewer interface
func (pa *PipelineAdapter) ReviewPR(ctx context.Context, req *domain.ReviewRequest) (*domain.ReviewResult, error) {
	slog.Info("Pipeline: Starting review", "pr_id", req.PR.ID)

	pipelineReq := ReviewRequest{
		PR:           *req.PR,
		LatestCommit: req.PR.LatestCommit,
		DegradeHint:  req.DegradeHint,
	}

	// 1. Stage 1: Diff Extraction
	var changes []FileChange
	if cached, ok := req.CachedStage1.([]FileChange); ok && cached != nil {
		changes = cached
		slog.Info("Stage 1: Using cached diff results", "pr_id", req.PR.ID)
	} else {
		var err error
		slog.Info("Stage 1: Starting Diff Extraction", "pr_id", req.PR.ID)
		changes, err = pa.pipeline.stage1.ExtractDiffs(ctx, pipelineReq)
		if err != nil {
			return nil, fmt.Errorf("stage 1 failed: %w", err)
		}
		req.CachedStage1 = changes
	}

	if len(changes) == 0 {
		return &domain.ReviewResult{
			Comments: []domain.ReviewComment{},
			Score:    100,
			Summary:  "No relevant changes found in this PR.",
			Model:    pa.pipeline.cfg.LLM.Model,
		}, nil
	}

	// 2. Stage 2: Context Collection
	var contextFiles []FileContent
	if cached, ok := req.CachedStage2.([]FileContent); ok && cached != nil {
		contextFiles = cached
		slog.Info("Stage 2: Using cached context results", "pr_id", req.PR.ID)
	} else {
		var err error
		slog.Info("Stage 2: Starting Context Collection", "pr_id", req.PR.ID)
		contextFiles, err = pa.pipeline.stage2.CollectContext(ctx, pipelineReq, changes)
		if err != nil {
			slog.Warn("stage 2 partially failed", "error", err)
			// Proceed even if context collection fails, using empty context
		}
		req.CachedStage2 = contextFiles
	}

	// 3. Stage 3: Direct Review
	result, err := pa.pipeline.stage3.Review(ctx, pipelineReq, changes, contextFiles)
	if err != nil {
		return nil, fmt.Errorf("stage 3 failed: %w", err)
	}

	result.Model = pa.pipeline.cfg.LLM.Model
	return result, nil
}

// Name returns the name of the reviewer
func (pa *PipelineAdapter) Name() string {
	return "pipeline"
}
