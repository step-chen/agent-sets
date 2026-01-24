package pipeline

import (
	"context"
	"fmt"
	"log/slog"
	"sort"

	"pr-review-automation/internal/domain"
)

// ReviewFunc is the function signature for the core review logic
type ReviewFunc func(ctx context.Context, req ReviewRequest, changes []FileChange, contextFiles []FileContent) (*domain.ReviewResult, error)

// ChunkReviewer handles the logic for splitting a large review into smaller chunks by file
type ChunkReviewer struct {
	maxTokens int
}

// NewChunkReviewer creates a new ChunkReviewer
func NewChunkReviewer(maxTokens int) *ChunkReviewer {
	return &ChunkReviewer{
		maxTokens: maxTokens,
	}
}

// ReviewChunked splits the changes and contextFiles into chunks and aggregates the results
func (cr *ChunkReviewer) ReviewChunked(
	ctx context.Context,
	req ReviewRequest,
	changes []FileChange,
	contextFiles []FileContent,
	baseSystemPrompt string,
	reviewFunc ReviewFunc,
) (*domain.ReviewResult, error) {

	// 1. Group files (Change + Context) by file path
	// This ensures we keep diff and context for the same file together.
	type FileGroup struct {
		Path    string
		Diff    FileChange
		Context FileContent
		Tokens  int
	}

	groups := make(map[string]*FileGroup)
	for _, c := range changes {
		groups[c.Path] = &FileGroup{
			Path: c.Path,
			Diff: c,
		}
	}
	for _, c := range contextFiles {
		if g, ok := groups[c.Path]; ok {
			g.Context = c
		} else {
			// This is an extra context file not in changes?
			// For now, we only care about context files that are related to changes or extra context.
			// Treat it as a separate group
			groups[c.Path] = &FileGroup{
				Path:    c.Path,
				Context: c,
			}
		}
	}

	// Calculate tokens for each group
	baseTokens := EstimateTokens(baseSystemPrompt)
	availableTokens := cr.maxTokens - baseTokens
	// Safety buffer
	availableTokens = int(float64(availableTokens) * 0.9)

	if availableTokens <= 0 {
		return nil, fmt.Errorf("base prompt too large for token limit")
	}

	for _, g := range groups {
		diffTokens := 0
		for _, line := range g.Diff.HunkLines {
			diffTokens += EstimateTokens(line)
		}
		g.Tokens = diffTokens + EstimateTokens(g.Context.Content)
	}

	// 2. Create Chunks
	var chunks [][]*FileGroup
	var currentChunk []*FileGroup
	currentTokens := 0

	// Sort groups for deterministic chunking
	var sortedKeys []string
	for k := range groups {
		sortedKeys = append(sortedKeys, k)
	}
	sort.Strings(sortedKeys)

	for _, k := range sortedKeys {
		g := groups[k]
		if g.Tokens > availableTokens {
			// Single file is too large!
			// We handle this by putting it in its own chunk and letting LLM truncate or hoping for best.
			// Ideally, we fall back to L3 (Diff Only) for this specific file, but here we just process it.
			slog.Warn("Single file group exceeds token limit", "path", g.Path, "tokens", g.Tokens, "limit", availableTokens)
			if len(currentChunk) > 0 {
				chunks = append(chunks, currentChunk)
				currentChunk = nil
				currentTokens = 0
			}
			chunks = append(chunks, []*FileGroup{g})
			continue
		}

		if currentTokens+g.Tokens > availableTokens {
			if len(currentChunk) > 0 {
				chunks = append(chunks, currentChunk)
			}
			currentChunk = []*FileGroup{g}
			currentTokens = g.Tokens
		} else {
			currentChunk = append(currentChunk, g)
			currentTokens += g.Tokens
		}
	}
	if len(currentChunk) > 0 {
		chunks = append(chunks, currentChunk)
	}

	slog.Info("L2 Chunking Plan", "total_files", len(groups), "chunks", len(chunks))

	// 3. Process Chunks
	var aggregatedResult domain.ReviewResult
	aggregatedResult.Summary = "## Chunked Review Summary\n\n"

	for i, chunk := range chunks {
		slog.Info("Processing Chunk", "index", i+1, "total", len(chunks), "files", len(chunk))

		// Convert back to changes and context
		var chunkChanges []FileChange
		var chunkContext []FileContent
		for _, g := range chunk {
			if g.Diff.Path != "" {
				chunkChanges = append(chunkChanges, g.Diff)
			}
			if g.Context.Path != "" {
				chunkContext = append(chunkContext, g.Context)
			}
		}

		res, err := reviewFunc(ctx, req, chunkChanges, chunkContext)
		if err != nil {
			slog.Error("Failed to review chunk", "index", i+1, "error", err)
			aggregatedResult.Summary += fmt.Sprintf("- **Chunk %d Failed**: %v\n", i+1, err)
			continue
		}

		// Merge Results
		aggregatedResult.Comments = append(aggregatedResult.Comments, res.Comments...)
		aggregatedResult.Score += res.Score // We need to average this later
		aggregatedResult.Summary += fmt.Sprintf("### Chunk %d\n%s\n\n", i+1, res.Summary)
	}

	if len(chunks) > 0 {
		aggregatedResult.Score /= len(chunks)
	}

	return &aggregatedResult, nil
}
