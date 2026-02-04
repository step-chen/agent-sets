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
	File     string       `json:"path" jsonschema:"minLength=1,required"`
	Line     FlexibleLine `json:"line" jsonschema:"required"`
	Comment  string       `json:"message" jsonschema:"minLength=1,required"`
	Severity string       `json:"severity,omitempty" jsonschema:"enum=CRITICAL,enum=WARNING,enum=INFO,enum=NIT,required,description=CRITICAL: bugs/crashes/security; WARNING: performance/logic gaps; INFO: style/best-practices"`
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

	// 3. Fallback/Error
	return nil
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
	DegradeHint        int // 0=None, 1=Truncate, 2=Drop

	// Stage 3 cache (used for retry)
	// Use interface{} to avoid domain -> pipeline cyclic dependency (pipeline.FileChange/FileContent)
	// Cast when using: *[]pipeline.FileChange and *[]pipeline.FileContent
	CachedStage1 interface{} `json:"-"`
	CachedStage2 interface{} `json:"-"`
}

// ReviewResult represents the outcome of a review
type ReviewResult struct {
	Comments []ReviewComment `json:"comments" jsonschema:"required"`
	Score    int             `json:"score" jsonschema:"minimum=0,maximum=100,required"`
	Summary  string          `json:"summary" jsonschema:"required"`
	Model    string
}

// Path constants migrated from config package to avoid dependency cycles
const (
	// PathPrefixGitSource is the standard Git source prefix
	PathPrefixGitSource = "a/"
	// PathPrefixGitDestination is the standard Git destination prefix
	PathPrefixGitDestination = "b/"
)

// NormalizePath normalizes a file path by removing common VCS prefixes (Git/SVN)
// and ensuring standard separators.
func NormalizePath(path string) string {
	// Standardize separators to forward slashes
	path = strings.ReplaceAll(path, "\\", "/")

	// List of prefixes to strip
	prefixes := []string{
		PathPrefixGitSource,
		PathPrefixGitDestination,
	}

	for _, p := range prefixes {
		path = strings.TrimPrefix(path, p)
	}

	return path
}
