package config

// Diff processing markers
const (
	MarkerTruncated  = "\n\n[... TRUNCATED FOR TOKEN LIMIT ...]"
	MarkerOmitted    = " [... context lines omitted ...]"
	MarkerDeleted    = "- [... %d lines deleted ...]"
	TruncatedSuffix  = "... [TRUNCATED]"
	MaxCommentLength = 500
)

// AI Review Markers
const (
	// MarkerAIReviewPrefix is the HTML comment start for AI metadata
	MarkerAIReviewPrefix = "<!-- ai-review::"
	// MarkerAIReviewSuffix is the HTML comment end
	MarkerAIReviewSuffix = "-->"
	// MarkerAIReviewVisible is the visible Markdown identifier
	MarkerAIReviewVisible = "**AI Review**"

	// New marker types
	MarkerTypeFile    = "file"
	MarkerTypeSummary = "summary"
)

// Deduplication Key Formats
const (
	// DedupeKeyFileLineFormat: file:line
	DedupeKeyFileLineFormat = "%s:%d"
	// DedupeKeySemanticFormat: file:content_prefix
	DedupeKeySemanticFormat = "%s:%s"
)

// MCP Server Names
const (
	MCPServerBitbucket  = "bitbucket"
	MCPServerJira       = "jira"
	MCPServerConfluence = "confluence"
)

// Tool Semantic Keys
const (
	ToolKeyGetDiff        = "get_diff"
	ToolKeyGetComments    = "get_comments"
	ToolKeyAddComment     = "add_comment"
	ToolKeyGetFileContent = "get_file_content"
	ToolKeyGetChanges     = "get_changes"
	ToolKeyGetPullRequest = "get_pull_request"
)

// RequiredToolKeys lists the tool keys that must be configured
var RequiredToolKeys = []string{
	ToolKeyGetDiff,
	ToolKeyGetComments,
	ToolKeyAddComment,
	ToolKeyGetFileContent,
}
