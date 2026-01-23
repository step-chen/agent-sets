package agent

import (
	"context"

	"pr-review-automation/internal/client"
	"pr-review-automation/internal/config"

	"google.golang.org/adk/model"
)

// Reviewer defines the interface for PR review agents
type Reviewer interface {
	// ReviewPR performs a PR review and returns the result
	ReviewPR(ctx context.Context, req *ReviewRequest) (*ReviewResult, error)
	// Name returns the name of the reviewer backend
	Name() string
}

// NewReviewer creates a Reviewer based on configuration
func NewReviewer(cfg config.AgentConfig, llm model.LLM, mcpClient *client.MCPClient,
	promptLoader *PromptLoader, modelName string) (Reviewer, error) {

	// Resolve backend - support both new Backend and deprecated DirectMode
	backend := cfg.Backend
	if backend == "" {
		if cfg.DirectMode {
			backend = "direct"
		} else {
			backend = "adk" // default
		}
	}

	switch backend {
	case "langchain":
		return NewLangChainAgent(cfg, llm, mcpClient, promptLoader, modelName)
	case "direct":
		// Direct mode uses the same PRReviewAgent but forces direct path
		cfg.DirectMode = true
		return NewPRReviewAgent(llm, mcpClient, promptLoader, modelName, cfg)
	default: // "adk" or any other value
		return NewPRReviewAgent(llm, mcpClient, promptLoader, modelName, cfg)
	}
}
