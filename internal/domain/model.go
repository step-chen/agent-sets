package domain

import (
	"encoding/json"
	"fmt"
	"strings"
)

// PullRequest represents the core domain model for a Pull Request.
// It serves as the canonical data structure across the application (Webhook -> Processor -> Agent).
type PullRequest struct {
	ID           string
	ProjectKey   string
	RepoSlug     string
	Title        string
	Description  string
	Author       string
	LatestCommit string // Latest commit SHA for tracking reviewed versions
	WebURL       string // Full URL to the pull request in the web interface
	// SourceBranch and TargetBranch can be added here if needed in the future
}

// IsValid checks if the PullRequest has the minimum required fields to proceed.
func (pr *PullRequest) IsValid() bool {
	return pr.ID != "" && pr.ProjectKey != "" && pr.RepoSlug != ""
}

const (
	CommentSeverityInfo     = "INFO"
	CommentSeverityWarning  = "WARNING"
	CommentSeverityCritical = "CRITICAL"
	CommentSeverityNit      = "NIT"
)

// ReviewComment represents a single review comment
type ReviewComment struct {
	File     string       `json:"path"`
	Line     FlexibleLine `json:"line"`
	Comment  string       `json:"message"`
	Severity string       `json:"severity,omitempty"`
	Marker   string       `json:"marker,omitempty"` // Internal use for deduplication
}

// FlexibleLine handles both int and []int JSON input, resolving to a single int anchor.
type FlexibleLine int

func (l *FlexibleLine) UnmarshalJSON(data []byte) error {
	// 1. Try single int
	var single int
	if err := json.Unmarshal(data, &single); err == nil {
		*l = FlexibleLine(single)
		return nil
	}

	// 2. Try array of ints (e.g. [4, 5])
	var arr []int
	if err := json.Unmarshal(data, &arr); err == nil {
		if len(arr) > 0 {
			// Strategy: Anchor to the start of the range
			*l = FlexibleLine(arr[0])
		} else {
			*l = 0
		}
		return nil
	}

	// 3. Fallback/Error (optional: try string parsing if LLM sends "4-5")
	return nil // Logic: treat as 0 (PR comment) if parse fails, or return error
}

// Fingerprint generates a semantic fingerprint for the comment.
// It combines the file path and the first 50 characters of the comment (lowercased)
// to identify duplicate comments regardless of minor line number shifts.
func (c *ReviewComment) Fingerprint() string {
	content := strings.ToLower(strings.TrimSpace(c.Comment))
	if len(content) > 50 {
		content = content[:50]
	}
	return fmt.Sprintf("%s:%s", c.File, content)
}

// IsHighSeverity checks if the comment represents a critical issue or warning.
func (c *ReviewComment) IsHighSeverity() bool {
	s := strings.ToUpper(c.Severity)
	return s == CommentSeverityCritical || s == CommentSeverityWarning
}

// ReviewRequest represents a request to review a PR
type ReviewRequest struct {
	PR                 *PullRequest
	HistoricalComments []ReviewComment
}

// ReviewResult represents the outcome of a review
type ReviewResult struct {
	Comments []ReviewComment `json:"comments"`
	Score    int             `json:"score"`
	Summary  string          `json:"summary"`
	Model    string
}
