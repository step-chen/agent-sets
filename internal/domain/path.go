package domain

import "strings"

// Path constants migrated from config package to avoid dependency cycles
const (
	// PathPrefixGitSource is the standard Git source prefix
	PathPrefixGitSource = "a/"
	// PathPrefixGitDestination is the standard Git destination prefix
	PathPrefixGitDestination = "b/"
	// PathPrefixSVNSource is the standard SVN source prefix
	PathPrefixSVNSource = "src/trunk/"
	// PathPrefixSVNDest is the standard SVN destination prefix
	PathPrefixSVNDest = "dst/trunk/"
	// PathPrefixSVNSourceURI is the standard SVN source URI prefix
	PathPrefixSVNSourceURI = "src://trunk/"
	// PathPrefixSVNDestURI is the standard SVN destination URI prefix
	PathPrefixSVNDestURI = "dst://trunk/"
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
		PathPrefixSVNSourceURI,
		PathPrefixSVNDestURI,
		PathPrefixSVNSource,
		PathPrefixSVNDest,
		"src://",
		"dst://",
		"trunk/",
	}

	for _, p := range prefixes {
		path = strings.TrimPrefix(path, p)
	}

	return path
}
