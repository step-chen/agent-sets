package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	// PullRequestTotal counts the total number of PRs processed, labeled by status.
	PullRequestTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "agent_pull_requests_total",
		Help: "The total number of processed pull requests",
	}, []string{"status"}) // status: success, failed

	// WebhookRequests counts incoming webhooks, labeled by status.
	WebhookRequests = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "agent_webhook_requests_total",
		Help: "The total number of received webhook requests",
	}, []string{"status"}) // status: accepted, dropped, invalid, ignored

	// ProcessingDuration measures the time taken to process a PR (end-to-end).
	ProcessingDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "agent_processing_duration_seconds",
		Help:    "Time taken to process a pull request",
		Buckets: prometheus.DefBuckets,
	}, []string{"result"}) // result: success, error

	// MCPToolCalls counts MCP tool executions
	MCPToolCalls = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "agent_mcp_tool_calls_total",
		Help: "The total number of MCP tool calls",
	}, []string{"server", "tool", "status"}) // status: success, error

	// CommentPostFailures counts failed comment posts
	CommentPostFailures = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "pr_review_comment_failures_total",
		Help: "Total number of failed comment posts to Bitbucket",
	}, []string{"reason"})

	// PayloadParseFailures counts failed payload parsing attempts
	PayloadParseFailures = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "webhook_payload_parse_failures_total",
		Help: "Total number of webhook payloads that failed to parse",
	}, []string{"failure_type"}) // failure_type: gjson, llm, both
)
