package bitbucket

// pruneKeys defines the top-level fields or shared keys that should be pruned
var pruneKeys = map[string]bool{
	// Webhook / Payload level
	"actor":        true, // Redundant with author
	"reviewers":    true, // Not needed for AI review content
	"participants": true, // Often empty or noise
	"links":        true, // AI cannot access these HATEOAS links

	// Nested object fields (shared across User, Repo, etc. if simple pruning applied)
	"permittedOperations": true,
	"anchored":            true,
	"version":             true,
	"state":               true, // Comment state (OPEN) is default/noise
	"markup":              true, // We use 'raw' content
	"html":                true, // We use 'raw' content

	// Repository metadata
	"archived":      true,
	"public":        true,
	"forkable":      true,
	"hierarchyId":   true,
	"scmId":         true,
	"statusMessage": true,

	// Ref objects
	"latestCommit": true,
}

// ShouldPrune checks if a key should be pruned
func ShouldPrune(key string) bool {
	return pruneKeys[key]
}
