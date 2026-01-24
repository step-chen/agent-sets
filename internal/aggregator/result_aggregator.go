package aggregator

import (
	"fmt"
	"pr-review-automation/internal/config"
	"pr-review-automation/internal/domain"
	"strings"
)

// ChunkReviewResult represents the review result of a single chunk
type ChunkReviewResult struct {
	ChunkID     int
	TotalChunks int
	Comments    []domain.ReviewComment
	Score       int
	Summary     string
	Error       error
}

// ResultAggregator combines results from multiple chunk reviews
type ResultAggregator struct{}

// NewResultAggregator creates a new ResultAggregator
func NewResultAggregator() *ResultAggregator {
	return &ResultAggregator{}
}

// Aggregate combines multiple chunk review results into a single result
func (a *ResultAggregator) Aggregate(results []ChunkReviewResult) *domain.ReviewResult {
	if len(results) == 0 {
		return &domain.ReviewResult{
			Score:   0,
			Summary: "No review results available.",
		}
	}

	var allComments []domain.ReviewComment
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

	return &domain.ReviewResult{
		Comments: dedupedComments,
		Score:    avgScore,
		Summary:  combinedSummary,
	}
}

// deduplicateComments removes duplicate comments (same file + same content)
func (a *ResultAggregator) deduplicateComments(comments []domain.ReviewComment) []domain.ReviewComment {
	seen := make(map[string]bool)
	var result []domain.ReviewComment

	for _, c := range comments {
		key := c.Fingerprint()
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
		sb.WriteString(fmt.Sprintf(config.ReportChunkedHeader, totalChunks))
	}

	if len(errors) > 0 {
		sb.WriteString(config.ReportPartialWarning)
		for _, e := range errors {
			sb.WriteString(fmt.Sprintf("- %s\n", e))
		}
		sb.WriteString("\n")
	}

	if len(summaries) == 0 {
		sb.WriteString(config.ReportNoSummary)
		return sb.String()
	}

	// If only one chunk succeeded, use its summary directly
	if len(summaries) == 1 {
		return summaries[0]
	}

	// Multiple summaries: combine them
	sb.WriteString(config.ReportSummaryHeader)
	for _, s := range summaries {
		sb.WriteString(fmt.Sprintf("- %s\n", s))
	}

	return sb.String()
}
