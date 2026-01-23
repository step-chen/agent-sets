package config

// Backend types
const (
	BackendADK       = "adk"
	BackendLangChain = "langchain"
	BackendDirect    = "direct"
)

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
	MarkerAIReviewPrefix = "<!-- ai-review:"
	// MarkerAIReviewSuffix is the HTML comment end
	MarkerAIReviewSuffix = "-->"
	// MarkerAIReviewVisible is the visible Markdown identifier
	MarkerAIReviewVisible = "**AI Review**"
)

// Deduplication Key Formats
const (
	// DedupeKeyFileLineFormat: file:line
	DedupeKeyFileLineFormat = "%s:%d"
	// DedupeKeySemanticFormat: file:content_prefix
	DedupeKeySemanticFormat = "%s:%s"
)

// Report formatting
const (
	ReportChunkedHeader  = "**Reviewed in %d chunks**\n\n"
	ReportPartialWarning = "⚠️ **Partial Review** (some chunks failed):\n"
	ReportSummaryHeader  = "**Summary by section:**\n"
	ReportNoSummary      = "No detailed summary available."
)

// Token limit error keywords (internal use only, not configurable)
var TokenLimitErrorKeywords = []string{
	"context_length_exceeded",
	"maximum context length",
	"context window",
	"token limit",
	"too many tokens",
}

// Path cleaning prefixes
const (
	PathPrefixGitSource      = "a/"
	PathPrefixGitDestination = "b/"
	PathPrefixSVNSource      = "src/trunk/"
	PathPrefixSVNDest        = "dst/trunk/"
	PathPrefixSVNSourceURI   = "src://trunk/"
	PathPrefixSVNDestURI     = "dst://trunk/"
)

// MCP Server Names
const (
	MCPServerBitbucket  = "bitbucket"
	MCPServerJira       = "jira"
	MCPServerConfluence = "confluence"
)

// MCP Tool Names
const (
	// Bitbucket Tools
	ToolBitbucketGetDiff        = "bitbucket_get_pull_request_diff"
	ToolBitbucketGetComments    = "bitbucket_get_pull_request_comments"
	ToolBitbucketAddComment     = "bitbucket_add_pull_request_comment"
	ToolBitbucketGetChanges     = "bitbucket_get_pull_request_changes"
	ToolBitbucketGetFileContent = "bitbucket_get_file_content"
	ToolBitbucketGetPullRequest = "bitbucket_get_pull_request"
)

// Tool Sets
var (
	// ChunkedReviewAllowedTools is the minimal toolset for chunked PR review
	ChunkedReviewAllowedTools = []string{ToolBitbucketGetFileContent}
)
