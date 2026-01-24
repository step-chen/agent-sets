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
	cfg           config.DegradationConfig
	maxTokens     int
	chunkReviewer *ChunkReviewer
}

// NewDegradationManager creates a new DegradationManager
func NewDegradationManager(cfg config.DegradationConfig, maxTokens int, chunkReviewer *ChunkReviewer) *DegradationManager {
	return &DegradationManager{
		cfg:           cfg,
		maxTokens:     maxTokens,
		chunkReviewer: chunkReviewer,
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
		"context", contextTokens)

	// Thresholds
	threshold80 := int(float64(dm.maxTokens) * 0.8)
	threshold100 := dm.maxTokens

	// Case 0: Within safe limits
	if totalTokens <= threshold80 {
		return reviewFunc(ctx, req, changes, contextFiles)
	}

	// Case 1: L1 - Truncate Context (if <= 100% or just over 80%)
	// We try this if we are between 80% and 120% (giving some buffer for L1 to succeed)
	// Actually, if we are > 80%, we should try L1 first.
	if totalTokens <= int(float64(dm.maxTokens)*1.2) {
		slog.Warn("Token limit warning (>80%), applying L1 degradation (Context Truncation)")
		reducedContext := dm.applyL1Truncation(contextFiles)

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
func (dm *DegradationManager) applyL1Truncation(contextFiles []FileContent) []FileContent {
	var reduced []FileContent
	// Map changes by path for quick lookup
	// changesMap := make(map[string]FileChange)
	// for _, c := range changes {
	// 	changesMap[c.Path] = c
	// }

	// For P0, we implement a simple "head/tail" or "max lines" truncation for context files.
	// Since mapping diff lines to context lines precisely requires parsing the patch again,
	// we will limit each context file to the first N lines plus last M lines or just first N lines.
	// Let's implement specific "Context Lines" from config if we can.
	// Since we don't have the logic to extract "relevant" lines easily here without code complexity,
	// we will simply truncate large context files to a fixed size limit (e.g., 200 lines).

	limit := dm.cfg.L1ContextLines * 2 // Heuristic: 2 * context lines ~ 100 lines total? No, L1ContextLines is "around changes".
	if limit < 100 {
		limit = 100
	}

	for _, cf := range contextFiles {
		lines := strings.Split(cf.Content, "\n")
		if len(lines) > limit {
			// Keep first K lines
			truncated := strings.Join(lines[:limit], "\n")
			truncated += fmt.Sprintf("\n... (truncated %d lines) ...", len(lines)-limit)
			reduced = append(reduced, FileContent{
				Path:    cf.Path,
				Content: truncated,
			})
		} else {
			reduced = append(reduced, cf)
		}
	}
	return reduced
}
