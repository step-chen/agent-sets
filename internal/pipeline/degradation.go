package pipeline

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"pr-review-automation/internal/config"
	"pr-review-automation/internal/domain"
)

// DegradationManager handles token limit degradation strategies
type DegradationManager struct {
	cfg            config.DegradationConfig
	maxTokens      int
	chunkReviewer  *ChunkReviewer
	contextReducer *ContextReducer
}

// NewDegradationManager creates a new DegradationManager
func NewDegradationManager(cfg config.DegradationConfig, maxTokens int, chunkReviewer *ChunkReviewer) *DegradationManager {
	return &DegradationManager{
		cfg:            cfg,
		maxTokens:      maxTokens,
		chunkReviewer:  chunkReviewer,
		contextReducer: NewContextReducer(),
	}
}

// EstimateTokens provides a rough estimate of token count (char count / 3.5)
func EstimateTokens(text string) int {
	return int(float64(len(text)) / 3.5)
}

// ApplyStrategy determines and applies the appropriate degradation strategy
func (dm *DegradationManager) ApplyStrategy(
	ctx context.Context,
	req ReviewRequest,
	changes []FileChange,
	contextFiles []FileContent,
	promptTemplate string,
	baseSystemPrompt string,
	reviewFunc ReviewFunc, // Callback for standard review
) (*domain.ReviewResult, error) {

	// 1. Calculate base token load (System Prompt + User Message + Diff + Context)
	// We estimate based on the actual content we plan to send.
	// Note: precise accounting is hard without actually building the full prompt,
	// so we use a safe heuristic on the components.

	baseTokens := EstimateTokens(baseSystemPrompt)
	diffTokens := 0
	for _, c := range changes {
		for _, line := range c.HunkLines {
			diffTokens += EstimateTokens(line)
		}
	}
	contextTokens := 0
	for _, c := range contextFiles {
		contextTokens += EstimateTokens(c.Content)
	}

	totalTokens := baseTokens + diffTokens + contextTokens
	slog.Info("Token Estimation",
		"total", totalTokens,
		"limit", dm.maxTokens,
		"base", baseTokens,
		"diff", diffTokens,
		"context", contextTokens,
		"degrade_hint", req.DegradeHint)

	// Force degradation based on external hint (from WorkerPool retries)
	if req.DegradeHint >= 2 {
		slog.Warn("Forcing L3 degradation (Diff Only) based on DegradeHint")
		return reviewFunc(ctx, req, changes, []FileContent{})
	}

	if req.DegradeHint == 1 {
		slog.Warn("Forcing L1 degradation (Context Truncation) based on DegradeHint")
		// Apply L1 Truncation and proceed
		// For forced L1, we assume we want to fit in MaxTokens or just aggressive reduction?
		// Stick to fitting in MaxTokens.
		// FORCE REDUCTION: If we are here, standard limits might have failed (timeout).
		// We cut the context budget in half to ensure significantly smaller payload.
		available := dm.maxTokens - baseTokens - diffTokens
		contextBudget := available / 2
		if contextBudget < 0 {
			contextBudget = 0
		}

		reducedContext := dm.applyContextReduction(contextFiles, contextBudget)
		slog.Info("L1 degradation applied (forced)", "original_files", len(contextFiles), "reduced_files", len(reducedContext))
		return reviewFunc(ctx, req, changes, reducedContext)
	}

	// Thresholds
	threshold80 := int(float64(dm.maxTokens) * 0.8)
	threshold100 := dm.maxTokens

	// Case 0: Within safe limits
	if totalTokens <= threshold80 {
		result, err := reviewFunc(ctx, req, changes, contextFiles)
		if err == nil {
			return result, nil
		}

		// [Smart Retry]: If timeout occurs (DeadlineExceeded) and we have not already degraded to L3,
		// and the parent context is NOT dead, try L3 degradation.
		if isTimeoutError(err) && ctx.Err() == nil {
			slog.Warn("Standard review timed out, attempting smart retry with L3 (Diff Only)")
			// Fallthrough to L3 logic
		} else {
			return nil, err
		}
	}

	// Case 1: L1 - Truncate Context (if <= 100% or just over 80%)
	// We try this if we are between 80% and 120% (giving some buffer for L1 to succeed)
	// Actually, if we are > 80%, we should try L1 first.
	if totalTokens <= int(float64(dm.maxTokens)*1.2) {
		slog.Warn("Token limit warning (>80%), applying L1 degradation (Context Truncation)")

		contextBudget := dm.maxTokens - baseTokens - diffTokens
		// Ensure non-negative (though diffTokens usually small compared to context, technically could exceed)
		if contextBudget < 0 {
			contextBudget = 0
		}

		reducedContext := dm.applyContextReduction(contextFiles, contextBudget)

		// Re-estimate
		newContextTokens := 0
		for _, c := range reducedContext {
			newContextTokens += EstimateTokens(c.Content)
		}
		newTotal := baseTokens + diffTokens + newContextTokens

		if newTotal <= threshold100 {
			slog.Info("L1 degradation successful", "new_total", newTotal)
			return reviewFunc(ctx, req, changes, reducedContext)
		}
		slog.Warn("L1 degradation insufficient", "new_total", newTotal)
	}

	// Case 2: L2 - Chunk by File
	if dm.cfg.L2ChunkByFile && dm.chunkReviewer != nil {
		slog.Warn("Token limit exceeded, applying L2 degradation (Chunk by File)")
		return dm.chunkReviewer.ReviewChunked(ctx, req, changes, contextFiles, baseSystemPrompt, reviewFunc)
	}

	// Case 3: L3 - Diff Only (Context Drop)
	if dm.cfg.L3DiffOnly {
		slog.Warn("Token limit critical, applying L3 degradation (Diff Only)")
		// Drop all context files
		return reviewFunc(ctx, req, changes, []FileContent{})
	}

	// Fallback/Fail
	return nil, fmt.Errorf("token limit exceeded (%d > %d) and no sufficient degradation strategy available", totalTokens, dm.maxTokens)
}

// applyL1Truncation filters context to only include lines around changes
// This is a simplified version; in reality, we'd need to parse the diff and map lines.
// For now, we'll do a simpler heuristic: Max N lines per file.
// applyContextReduction uses ContextReducer to fit context into budget
func (dm *DegradationManager) applyContextReduction(contextFiles []FileContent, budget int) []FileContent {
	return dm.contextReducer.Reduce(contextFiles, budget)
}

func isTimeoutError(err error) bool {
	if err == nil {
		return false
	}
	// Check for context.DeadlineExceeded wrapped in any way
	return strings.Contains(err.Error(), "context deadline exceeded") || strings.Contains(err.Error(), "timeout")
}
