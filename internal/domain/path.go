package domain

import "strings"

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
