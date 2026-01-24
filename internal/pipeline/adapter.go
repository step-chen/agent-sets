package pipeline

import (
	"context"
	"fmt"
	"log/slog"

	"pr-review-automation/internal/client"
	"pr-review-automation/internal/config"
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

	// Initialize stages
	p.stage1 = NewStage1(&cfg.Pipeline, mcpClient, llm, promptLoader)
	p.stage2 = NewStage2(&cfg.Pipeline, mcpClient, llm, promptLoader)
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
	}

	// 1. Stage 1: Diff Extraction
	changes, err := pa.pipeline.stage1.ExtractDiffs(ctx, pipelineReq)
	if err != nil {
		return nil, fmt.Errorf("stage 1 failed: %w", err)
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
	// Note: We currently don't use context files in Stage 3 prompt yet, but it's ready to be added.
	contextFiles, err := pa.pipeline.stage2.CollectContext(ctx, pipelineReq, changes)
	if err != nil {
		slog.Warn("stage 2 partially failed", "error", err)
		// Proceed even if context collection fails, using empty context
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
