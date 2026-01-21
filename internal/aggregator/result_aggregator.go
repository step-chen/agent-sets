package aggregator

import (
	"fmt"
	"strings"
)

// ChunkReviewResult represents the review result of a single chunk
type ChunkReviewResult struct {
	ChunkID     int
	TotalChunks int
	Comments    []ReviewComment
	Score       int
	Summary     string
	Error       error
}

// ReviewComment represents a single review comment
type ReviewComment struct {
	File    string `json:"file"`
	Line    int    `json:"line"`
	Comment string `json:"comment"`
}

// AggregatedResult represents the final combined review result
type AggregatedResult struct {
	Comments []ReviewComment
	Score    int
	Summary  string
	Model    string
}

// ResultAggregator combines results from multiple chunk reviews
type ResultAggregator struct{}

// NewResultAggregator creates a new ResultAggregator
func NewResultAggregator() *ResultAggregator {
	return &ResultAggregator{}
}

// Aggregate combines multiple chunk review results into a single result
func (a *ResultAggregator) Aggregate(results []ChunkReviewResult) *AggregatedResult {
	if len(results) == 0 {
		return &AggregatedResult{
			Score:   0,
			Summary: "No review results available.",
		}
	}

	var allComments []ReviewComment
	var validScores []int
	var summaries []string
	var errors []string

	for _, r := range results {
		if r.Error != nil {
			errors = append(errors, fmt.Sprintf("Chunk %d: %v", r.ChunkID, r.Error))
			continue
		}

		allComments = append(allComments, r.Comments...)
		validScores = append(validScores, r.Score)
		if r.Summary != "" {
			summaries = append(summaries, fmt.Sprintf("[Chunk %d/%d] %s", r.ChunkID, r.TotalChunks, r.Summary))
		}
	}

	// Deduplicate comments (same file + line)
	dedupedComments := a.deduplicateComments(allComments)

	// Calculate average score
	avgScore := 0
	if len(validScores) > 0 {
		total := 0
		for _, s := range validScores {
			total += s
		}
		avgScore = total / len(validScores)
	}

	// Combine summaries
	combinedSummary := a.combineSummaries(summaries, errors, len(results))

	return &AggregatedResult{
		Comments: dedupedComments,
		Score:    avgScore,
		Summary:  combinedSummary,
	}
}

// deduplicateComments removes duplicate comments (same file + line)
func (a *ResultAggregator) deduplicateComments(comments []ReviewComment) []ReviewComment {
	seen := make(map[string]bool)
	var result []ReviewComment

	for _, c := range comments {
		key := fmt.Sprintf("%s:%d", c.File, c.Line)
		if !seen[key] {
			seen[key] = true
			result = append(result, c)
		}
	}

	return result
}

// combineSummaries creates a unified summary from chunk summaries
func (a *ResultAggregator) combineSummaries(summaries []string, errors []string, totalChunks int) string {
	var sb strings.Builder

	if totalChunks > 1 {
		sb.WriteString(fmt.Sprintf("**Reviewed in %d chunks**\n\n", totalChunks))
	}

	if len(errors) > 0 {
		sb.WriteString("⚠️ **Partial Review** (some chunks failed):\n")
		for _, e := range errors {
			sb.WriteString(fmt.Sprintf("- %s\n", e))
		}
		sb.WriteString("\n")
	}

	if len(summaries) == 0 {
		sb.WriteString("No detailed summary available.")
		return sb.String()
	}

	// If only one chunk succeeded, use its summary directly
	if len(summaries) == 1 {
		return summaries[0]
	}

	// Multiple summaries: combine them
	sb.WriteString("**Summary by section:**\n")
	for _, s := range summaries {
		sb.WriteString(fmt.Sprintf("- %s\n", s))
	}

	return sb.String()
}
