package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"iter"
	"log/slog"
	"strings"
	"time"

	"pr-review-automation/internal/client"
	"pr-review-automation/internal/domain"
	"pr-review-automation/internal/metrics"

	"google.golang.org/adk/agent"
	"google.golang.org/adk/agent/llmagent"
	"google.golang.org/adk/model"
	"google.golang.org/adk/runner"
	"google.golang.org/adk/session"
	"google.golang.org/genai"
)

// PRReviewAgent represents a PR review agent powered by ADK-Go
type PRReviewAgent struct {
	llm            model.LLM
	sessionService session.Service
	mcpClient      *client.MCPClient
	promptLoader   *PromptLoader
	modelName      string
}

// ReviewResult represents the result of a PR review
type ReviewResult struct {
	Comments []ReviewComment `json:"comments"`
	Score    int             `json:"score"`
	Summary  string          `json:"summary"`
	Model    string          `json:"model,omitempty"`
}

// ReviewComment represents a single comment in a PR review
type ReviewComment struct {
	File    string `json:"file"`
	Line    int    `json:"line"`
	Comment string `json:"comment"`
}

// NewPRReviewAgent creates a new PR review agent factory
func NewPRReviewAgent(llm model.LLM, mcpClient *client.MCPClient, promptLoader *PromptLoader, modelName string) (*PRReviewAgent, error) {
	// Validation of dependencies
	if llm == nil {
		return nil, fmt.Errorf("llm is nil")
	}
	if mcpClient == nil {
		return nil, fmt.Errorf("mcp client is nil")
	}
	if promptLoader == nil {
		return nil, fmt.Errorf("prompt loader is nil")
	}

	return &PRReviewAgent{
		llm:            llm,
		sessionService: session.InMemoryService(),
		mcpClient:      mcpClient,
		promptLoader:   promptLoader,
		modelName:      modelName,
	}, nil
}

// ReviewPR processes a pull request using a fresh ADK agent instance
func (pra *PRReviewAgent) ReviewPR(ctx context.Context, pr *domain.PullRequest) (*ReviewResult, error) {
	start := time.Now()
	metricResult := "error" // Default to error, changed to success on success path
	defer func() {
		metrics.ProcessingDuration.WithLabelValues(metricResult).Observe(time.Since(start).Seconds())
	}()

	slog.Debug("Starting PR review",
		"repository", pr.RepoSlug,
		"pr_id", pr.ID,
		"title", pr.Title,
		"author", pr.Author,
	)

	// 1. Get FRESH Toolsets
	toolsets := pra.mcpClient.GetToolsets()
	if len(toolsets) == 0 {
		slog.Warn("no mcp toolsets available")
	}

	// 2. Load prompt (project from PR, language detection TBD)
	instruction, err := pra.promptLoader.Load(pr.ProjectKey, "default")
	if err != nil {
		return nil, fmt.Errorf("load prompt: %w", err)
	}

	// 3. Create Ephemeral Agent
	agentConfig := llmagent.Config{
		Name:        "pr-review-agent-" + pr.ID,
		Description: "Ephemeral PR review agent",
		Model:       &jsonLLM{LLM: pra.llm},
		Instruction: instruction,
		Toolsets:    toolsets,
	}

	adkAgent, err := llmagent.New(agentConfig)
	if err != nil {
		return nil, fmt.Errorf("create agent: %w", err)
	}

	slog.Debug("agent created", "toolsets", len(toolsets))

	// Convert PR info to a prompt
	prompt := fmt.Sprintf(`Please review the following pull request:

Repository: %v
Pull Request ID: %v
Title: %v
Description: %v
Author: %v

Analyze the changes and provide feedback on code quality, potential bugs, and adherence to best practices. 
You MUST use the provided tools to fetch the changed files and their diffs/content to perform the review. 
Do not guess the content. 
Also check if the changes align with any related Jira issues and follow Confluence documentation standards. 

Return your response in the following JSON format ONLY, do not include markdown formatting:
{
  "comments": [
    {
      "file": "filename.ext",
      "line": 123,
      "comment": "Detailed feedback about the code at this location"
    }
  ],
  "score": 85,
  "summary": "Overall assessment of the pull request"
}`,
		pr.RepoSlug,
		pr.ID,
		pr.Title,
		pr.Description,
		pr.Author,
	)

	// Create a runner
	r, err := runner.New(runner.Config{
		AppName:        "pr-review",
		Agent:          adkAgent,
		SessionService: pra.sessionService,
	})
	if err != nil {
		return nil, fmt.Errorf("create runner: %w", err)
	}

	// Session ID based on PR + timestamp
	sessionID := fmt.Sprintf("review-%v-%v-%d", pr.RepoSlug, pr.ID, time.Now().UnixNano())

	// Ensure session is created
	_, err = pra.sessionService.Create(ctx, &session.CreateRequest{
		AppName:   "pr-review",
		UserID:    "automation-user",
		SessionID: sessionID,
	})
	if err != nil && !strings.Contains(err.Error(), "already exists") {
		slog.Debug("create session failed", "session_id", sessionID, "error", err)
	}

	// CLEANUP: Delete session after review completes
	// This is critical to prevent memory leaks in InMemoryService
	defer func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := pra.sessionService.Delete(cleanupCtx, &session.DeleteRequest{
			SessionID: sessionID,
		}); err != nil {
			// Log as Error (not Warn) since failed cleanup leads to memory leak
			slog.Error("session cleanup failed",
				"session_id", sessionID,
				"error", err)
			// Track potential leaks via metrics
			metrics.PullRequestTotal.WithLabelValues("session_cleanup_failed").Inc()
		}
	}()

	msg := &genai.Content{
		Parts: []*genai.Part{
			{Text: prompt},
		},
	}

	var finalText string
	eventCount := 0

	slog.Debug("running agent", "session", sessionID)

	// Run the agent
	for event, err := range r.Run(ctx, "automation-user", sessionID, msg, agent.RunConfig{}) {
		eventCount++
		if err != nil {
			return nil, fmt.Errorf("agent exec: %w", err)
		}

		if event.IsFinalResponse() {
			slog.Debug("agent event", "count", eventCount, "final", true)
			for _, part := range event.LLMResponse.Content.Parts {
				finalText += part.Text
			}
		}
	}

	if finalText == "" {
		return nil, fmt.Errorf("no response content")
	}

	// Clean up markdown code blocks if present
	finalText = strings.TrimPrefix(finalText, "```json")
	finalText = strings.TrimPrefix(finalText, "```")
	finalText = strings.TrimSuffix(finalText, "```")
	finalText = strings.TrimSpace(finalText)

	var reviewResult ReviewResult
	if err := json.Unmarshal([]byte(finalText), &reviewResult); err != nil {
		slog.Error("parse json response failed", "error", err, "raw_text", finalText)
		return &ReviewResult{
			Score:   0,
			Summary: "Failed to parse agent response.",
		}, nil
	}

	slog.Info("PR review completed", "score", reviewResult.Score, "summary", reviewResult.Summary)
	metricResult = "success"
	metrics.PullRequestTotal.WithLabelValues("success").Inc()

	reviewResult.Model = pra.modelName
	return &reviewResult, nil
}

// jsonLLM is a wrapper around model.LLM that invalidates JSON mode
type jsonLLM struct {
	model.LLM
}

func (j *jsonLLM) GenerateContent(ctx context.Context, req *model.LLMRequest, streaming bool) iter.Seq2[*model.LLMResponse, error] {
	if req.Config == nil {
		req.Config = &genai.GenerateContentConfig{}
	}
	req.Config.ResponseMIMEType = "application/json"
	return j.LLM.GenerateContent(ctx, req, streaming)
}
