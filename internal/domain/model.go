package domain

// PullRequest represents the core domain model for a Pull Request.
// It serves as the canonical data structure across the application (Webhook -> Processor -> Agent).
type PullRequest struct {
	ID          string
	ProjectKey  string
	RepoSlug    string
	Title       string
	Description string
	Author      string
	// SourceBranch and TargetBranch can be added here if needed in the future
}

// IsValid checks if the PullRequest has the minimum required fields to proceed.
func (pr *PullRequest) IsValid() bool {
	return pr.ID != "" && pr.ProjectKey != "" && pr.RepoSlug != ""
}
