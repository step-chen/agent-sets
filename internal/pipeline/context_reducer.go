package pipeline

import (
	"fmt"
	"log/slog"
	"strings"

	codecontext "pr-review-automation/internal/context"
)

// ContextReducer manages token budget by reducing file content
type ContextReducer struct {
}

// NewContextReducer creates a new ContextReducer
func NewContextReducer() *ContextReducer {
	return &ContextReducer{}
}

// Reduce ensures the total context size is within budget
func (r *ContextReducer) Reduce(files []FileContent, budget int) []FileContent {
	// Separate diffs and context
	var diffFiles []FileContent
	var contextFiles []FileContent

	for _, f := range files {
		if f.IsDiffed {
			diffFiles = append(diffFiles, f)
		} else {
			contextFiles = append(contextFiles, f)
		}
	}

	// Step 1: Proactive Compression (Semantically compress ALL context files)
	var compressedContext []FileContent
	for _, f := range contextFiles {
		if f.Analysis != nil && len(f.Analysis.Chunks) > 0 {
			// Apply semantic compression
			compressed := r.compressUsingChunks(f.Content, f.Analysis.Chunks)
			f.Content = compressed + "\n// (Context reduced: bodies hidden)"
		}
		compressedContext = append(compressedContext, f)
	}

	// Calculate usage
	diffTokens := r.estimateTokens(diffFiles)
	contextTokens := r.estimateTokens(compressedContext)
	totalTokens := diffTokens + contextTokens

	slog.Info("ContextReducer: Proactive compression result",
		"diff_tokens", diffTokens,
		"context_tokens", contextTokens,
		"total_tokens", totalTokens,
		"budget", budget)

	if totalTokens <= budget {
		return append(diffFiles, compressedContext...)
	}

	// Step 2: Layered Degradation
	remainingBudget := budget - diffTokens // Ring-fence diffs

	if remainingBudget <= 0 {
		slog.Warn("Budget too tight even for diffs alone. Returning only diffs (potentially truncated by upstream).",
			"diff_tokens", diffTokens, "budget", budget)
		return diffFiles // L3: Diff Only (implicit)
	}

	// L1: Drop least important context files (simulated by truncation of list)
	// We assume contextFiles are sorted by importance/relevance (from Stage 2)
	// Just take as many as fit? Or fit as much as possible?
	// The requirement is "L1: Drop tail"

	validContext := []FileContent{}
	currentContextTokens := 0

	for _, f := range compressedContext {
		fTokens := estimateTokenCount(f.Content)
		if currentContextTokens+fTokens <= remainingBudget {
			validContext = append(validContext, f)
			currentContextTokens += fTokens
		} else {
			// Doesn't fit.
			// L2: Truncate lines of this file to fit remaining budget?
			// The plan says "L2: Line truncation".
			// Let's try to fit partial.

			left := remainingBudget - currentContextTokens
			if left >= 20 { // Minimal useful size
				truncated := r.truncateLines(f.Content, left)
				f.Content = truncated
				validContext = append(validContext, f)
				// Budget full
				break
			} else {
				// Drop this file
				slog.Info("ContextReducer: Dropping file due to budget", "path", f.Path)
			}
			break // Stop adding files
		}
	}

	slog.Info("ContextReducer: Degradation applied",
		"original_context", len(contextFiles),
		"kept_context", len(validContext))

	return append(diffFiles, validContext...)
}

// estimateTokens sums up tokens for all files
func (r *ContextReducer) estimateTokens(files []FileContent) int {
	total := 0
	for _, f := range files {
		total += estimateTokenCount(f.Content)
	}
	return total
}

// compressUsingChunks keeps only function signatures/headers
// This is a heuristic: it constructs a "skeleton" of the file.
func (r *ContextReducer) compressUsingChunks(source string, chunks []codecontext.Chunk) string {
	// Reconstruct file with only chunk headers
	// We need to know where the signature ends.
	// Chunk has StartLine, EndLine.
	// Naive approach: Take first line of the chunk?
	// Or first N chars?
	// Tree-sitter captures are better but we only have Chunk struct here.

	// Better: Keep the whole file but REPLACE chunk bodies with "..."
	// We can process lines.

	lines := strings.Split(source, "\n")
	resultLines := make([]string, 0, len(lines))

	// Map lines to chunks to skip bodies
	// chunks are 1-based lines.
	// Optimization: Sort chunks by start line (Tree-sitter usually gives sorted).

	currentLine := 1
	chunkIdx := 0

	for currentLine <= len(lines) {
		// Check if we are entering a chunk
		if chunkIdx < len(chunks) && currentLine == chunks[chunkIdx].StartLine {
			chunk := chunks[chunkIdx]

			// Append signature/header
			// How to get signature?
			// 1. Take the first line of the chunk (often enough for func declaration)
			// 2. Or take lines until we see '{' ?
			// 3. Or use a heuristic: first 1-2 lines of chunk.

			headerEnd := chunk.StartLine // Default to just first line

			// Simple heuristic: read lines until '{' or just take first line
			// Careful not to go past chunk.EndLine
			for i := chunk.StartLine; i <= chunk.EndLine; i++ {
				line := lines[i-1]
				resultLines = append(resultLines, line)
				if strings.Contains(line, "{") {
					headerEnd = i
					break
				}
				headerEnd = i
			}

			if headerEnd < chunk.EndLine {
				resultLines = append(resultLines, "    // ... implementation hidden ...")
				resultLines = append(resultLines, "}") // Close logic block
				// Skip to EndLine
				currentLine = chunk.EndLine + 1
			} else {
				// Chunk was short or no '{' found, kept all of it (or loop finished)
				currentLine = chunk.EndLine + 1
			}
			chunkIdx++
		} else {
			// Non-chunk line (imports, comments, whitespace)
			// Keep it
			resultLines = append(resultLines, lines[currentLine-1])
			currentLine++
		}
	}

	return strings.Join(resultLines, "\n")
}

// truncateLines keeps first N lines that fit in budget
func (r *ContextReducer) truncateLines(content string, budget int) string {
	lines := strings.Split(content, "\n")
	if len(lines) == 0 {
		return ""
	}

	// Simple heuristic: keep building until budget full
	var sb strings.Builder
	currentChars := 0

	for i, line := range lines {
		addedLen := len(line)
		if i > 0 {
			addedLen += 1 // for '\n'
		}

		// Check if adding this line exceeds budget
		// Use strict estimation on accumulated content
		if int(float64(currentChars+addedLen)/3.5) > budget {
			if i == 0 {
				return "" // Doesn't fit even one line
			}
			sb.WriteString(fmt.Sprintf("\n... (truncated remaining %d lines)", len(lines)-i))
			break
		}
		if i > 0 {
			sb.WriteString("\n")
		}
		sb.WriteString(line)
		currentChars += addedLen
	}
	return sb.String()
}

// Helper (duplicate from helpers.go but avoid import cycle or make public)
// We assume estimateTokenCount is available or we redefine strictly for reducer
func estimateTokenCount(text string) int {
	return int(float64(len(text)) / 3.5)
}
