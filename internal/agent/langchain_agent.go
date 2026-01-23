package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"pr-review-automation/internal/client"
	"pr-review-automation/internal/config"
	"pr-review-automation/internal/metrics"
	"pr-review-automation/internal/splitter"
	"pr-review-automation/internal/types"

	"github.com/tidwall/gjson"
	"github.com/tmc/langchaingo/agents"
	"github.com/tmc/langchaingo/chains"
	"github.com/tmc/langchaingo/llms/openai"
	"github.com/tmc/langchaingo/tools"
	"google.golang.org/adk/model"
)

// LangChainAgent implements Reviewer using LangChainGo
type LangChainAgent struct {
	llm          model.LLM
	mcpClient    *client.MCPClient
	promptLoader *PromptLoader
	modelName    string
	cfg          config.AgentConfig
}

// NewLangChainAgent creates a new LangChainGo-based agent
func NewLangChainAgent(cfg config.AgentConfig, llm model.LLM, mcpClient *client.MCPClient,
	promptLoader *PromptLoader, modelName string) (*LangChainAgent, error) {

	if llm == nil {
		return nil, fmt.Errorf("llm is nil")
	}
	if mcpClient == nil {
		return nil, fmt.Errorf("mcp client is nil")
	}
	if promptLoader == nil {
		return nil, fmt.Errorf("prompt loader is nil")
	}

	if cfg.MaxIterations <= 0 {
		cfg.MaxIterations = 20
	}

	return &LangChainAgent{
		llm:          llm,
		mcpClient:    mcpClient,
		promptLoader: promptLoader,
		modelName:    modelName,
		cfg:          cfg,
	}, nil
}

func (a *LangChainAgent) Name() string {
	return "langchain"
}

// ReviewPR implements Reviewer interface
func (a *LangChainAgent) ReviewPR(ctx context.Context, req *ReviewRequest) (*ReviewResult, error) {
	pr := req.PR
	slog.Info("Starting LangChain PR review", "pr_id", pr.ID)

	start := time.Now()
	metricResult := "error"
	defer func() {
		metrics.ProcessingDuration.WithLabelValues(metricResult + "_langchain").Observe(time.Since(start).Seconds())
	}()

	// 1. Fetch diff via MCP
	prID, _ := strconv.Atoi(pr.ID)
	diffResult, err := a.mcpClient.CallTool(ctx, config.MCPServerBitbucket, config.ToolBitbucketGetDiff, map[string]interface{}{
		"projectKey":    pr.ProjectKey,
		"repoSlug":      pr.RepoSlug,
		"pullRequestId": prID,
	})
	if err != nil {
		return nil, fmt.Errorf("fetch diff: %w", err)
	}

	diffStr := extractDiffString(diffResult)
	slog.Debug("langchain: diff fetched", "bytes", len(diffStr))
	if diffStr == "" {
		return nil, fmt.Errorf("empty diff from MCP")
	}

	// 2. Preprocess diff
	preprocessor := splitter.NewDiffPreprocessor(splitter.PreprocessOptions{
		MaxContextLines:  a.cfg.ChunkReview.ContextLines,
		FoldDeletesOver:  a.cfg.ChunkReview.FoldDeletesOver,
		RemoveBinaryDiff: true,
		RemoveWhitespace: a.cfg.ChunkReview.RemoveWhitespace,
	})
	diffStr = preprocessor.Preprocess(diffStr)

	// Truncate if too large
	maxChars := a.cfg.MaxDirectChars
	if maxChars == 0 {
		maxChars = 40000
	}
	if len(diffStr) > maxChars {
		diffStr = diffStr[:maxChars] + "\n\n[... TRUNCATED FOR TOKEN LIMIT ...]"
	}

	// 3. Load prompt
	language := "default"
	if changedFiles := FetchChangedFiles(ctx, a.mcpClient, pr); len(changedFiles) > 0 {
		language = DetectLanguage(changedFiles)
	}
	_, err = a.promptLoader.Load(pr.ProjectKey, language, map[string]interface{}{
		"ProjectKey": pr.ProjectKey,
		"RepoSlug":   pr.RepoSlug,
	})
	if err != nil {
		slog.Debug("prompt load warning", "error", err)
		// Continue with default behavior
	}

	// 4. Create LangChain LLM from OpenAI adapter
	llmAdapter, ok := a.llm.(*client.OpenAIAdapter)
	if !ok {
		return nil, fmt.Errorf("langchain agent requires OpenAIAdapter")
	}
	endpoint, apiKey := llmAdapter.GetConfig()

	lcLLM, err := openai.New(
		openai.WithModel(a.modelName),
		openai.WithBaseURL(endpoint),
		openai.WithToken(apiKey),
	)
	if err != nil {
		return nil, fmt.Errorf("create langchain llm: %w", err)
	}

	// 5. Build MCP tools for LangChain
	mcpTools := a.buildMCPTools()
	slog.Debug("langchain: tools registered", "count", len(mcpTools))

	// 6. Create agent and executor
	slog.Debug("langchain: creating agent", "max_iterations", a.cfg.MaxIterations)
	agent := agents.NewOneShotAgent(lcLLM, mcpTools,
		agents.WithMaxIterations(a.cfg.MaxIterations),
	)
	executor := agents.NewExecutor(agent)

	// 7. Build prompt
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Review PR #%s in %s/%s\n\n", pr.ID, pr.ProjectKey, pr.RepoSlug))
	sb.WriteString(fmt.Sprintf("Title: %s\nAuthor: %s\n\n", pr.Title, pr.Author))
	sb.WriteString("## Diff:\n```diff\n")
	sb.WriteString(diffStr)
	sb.WriteString("\n```\n\n")

	// Historical comments for deduplication
	if len(req.HistoricalComments) > 0 {
		sb.WriteString("## Known Issues (DO NOT DUPLICATE):\n")
		for _, c := range req.HistoricalComments {
			truncated := c.Comment
			if len(truncated) > 50 {
				truncated = truncated[:50] + "..."
			}
			sb.WriteString(fmt.Sprintf("- %s:%d %s\n", c.File, c.Line, truncated))
		}
		sb.WriteString("\n")
	}

	sb.WriteString("Review and output ONLY valid JSON:\n")
	sb.WriteString(`{"comments":[{"file":"path","line":123,"comment":"issue"}],"score":85,"summary":"verdict"}`)

	// 8. Run executor
	slog.Debug("langchain: executing agent", "input_len", sb.Len())
	response, err := chains.Call(ctx, executor, map[string]any{
		"input": sb.String(),
	})
	if err != nil {
		slog.Error("langchain: executor failed", "error", err)
		return nil, fmt.Errorf("executor call: %w", err)
	}
	slog.Debug("langchain: executor completed", "output_keys", len(response))

	// 9. Extract output
	output, ok := response["output"].(string)
	if !ok {
		return nil, fmt.Errorf("unexpected response type: %T", response["output"])
	}

	// 10. Parse response
	output = types.CleanJSONFromMarkdown(output)
	var result ReviewResult
	if err := json.Unmarshal([]byte(output), &result); err != nil {
		// Fallback extraction
		startIdx := strings.Index(output, "{")
		endIdx := strings.LastIndex(output, "}")
		if startIdx != -1 && endIdx != -1 && endIdx > startIdx {
			if err2 := json.Unmarshal([]byte(output[startIdx:endIdx+1]), &result); err2 == nil {
				result.Model = a.modelName + " (langchain)"
				metricResult = "success"
				return &result, nil
			}
		}
		return nil, fmt.Errorf("parse response: %w", err)
	}

	result.Model = a.modelName + " (langchain)"
	metricResult = "success"
	metrics.PullRequestTotal.WithLabelValues("success_langchain").Inc()

	slog.Info("LangChain PR review completed",
		"pr_id", pr.ID,
		"comments", len(result.Comments),
		"duration", time.Since(start))

	return &result, nil
}

// buildMCPTools creates LangChain tools from MCP definitions
func (a *LangChainAgent) buildMCPTools() []tools.Tool {
	var lcTools []tools.Tool

	// Get tool declarations from MCP client
	toolDefs := a.mcpClient.GetToolDeclarations()
	for serverName, toolList := range toolDefs {
		for _, t := range toolList {
			if t == nil {
				continue
			}
			// Wrap MCP tool as LangChain tool
			lcTool := &MCPToolWrapper{
				mcpClient:   a.mcpClient,
				toolName:    t.Name,
				description: t.Description,
				serverName:  serverName,
			}
			lcTools = append(lcTools, lcTool)
		}
	}

	return lcTools
}

// MCPToolWrapper wraps an MCP tool for LangChainGo
type MCPToolWrapper struct {
	mcpClient   *client.MCPClient
	toolName    string
	description string
	serverName  string
}

func (t *MCPToolWrapper) Name() string {
	return t.toolName
}

func (t *MCPToolWrapper) Description() string {
	return t.description
}

func (t *MCPToolWrapper) Call(ctx context.Context, input string) (string, error) {
	slog.Debug("langchain: tool call", "tool", t.toolName, "server", t.serverName, "input_len", len(input))

	// Parse input as JSON
	var args map[string]interface{}
	if err := json.Unmarshal([]byte(input), &args); err != nil {
		// If not JSON, treat as simple string input
		args = map[string]interface{}{"input": input}
		slog.Debug("langchain: tool input not JSON, using raw", "tool", t.toolName)
	}

	result, err := t.mcpClient.CallTool(ctx, t.serverName, t.toolName, args)
	if err != nil {
		slog.Error("langchain: tool call failed", "tool", t.toolName, "error", err)
		return "", err
	}

	// Convert result to string
	var outputLen int
	var output string
	if s, ok := result.(string); ok {
		output = s
		outputLen = len(s)
	} else {
		b, err := json.Marshal(result)
		if err != nil {
			output = fmt.Sprintf("%v", result)
		} else {
			output = string(b)
		}
		outputLen = len(output)
	}

	slog.Debug("langchain: tool call completed", "tool", t.toolName, "output_len", outputLen)
	return output, nil
}

// extractDiffString extracts diff string from MCP result
func extractDiffString(diffResult interface{}) string {
	if s, ok := diffResult.(string); ok {
		return s
	}
	b, err := json.Marshal(diffResult)
	if err != nil {
		return ""
	}
	diffStr := gjson.GetBytes(b, "content.0.text").String()
	if diffStr == "" {
		diffStr = gjson.GetBytes(b, "output").String()
	}
	return diffStr
}

// Ensure MCPToolWrapper implements tools.Tool
var _ tools.Tool = (*MCPToolWrapper)(nil)

// Ensure LangChainAgent implements Reviewer
var _ Reviewer = (*LangChainAgent)(nil)
