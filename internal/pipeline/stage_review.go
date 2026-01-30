package pipeline

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"pr-review-automation/internal/client"
	"pr-review-automation/internal/config"
	"pr-review-automation/internal/domain"

	"github.com/openai/openai-go"
	"github.com/openai/openai-go/shared"
)

// Stage3 implements the Direct Review stage
type Stage3 struct {
	cfg                *config.PipelineConfig
	mcpClient          *client.MCPClient
	llm                LLMClient
	promptLoader       *PromptLoader
	degradationManager *DegradationManager
}

// NewStage3 creates a new Stage3 instance
func NewStage3(cfg *config.PipelineConfig, mcpClient *client.MCPClient, llm LLMClient, promptLoader *PromptLoader) *Stage3 {
	chunkReviewer := NewChunkReviewer(cfg.Stage3Review.MaxContextTokens)
	dm := NewDegradationManager(cfg.Stage3Review.Degradation, cfg.Stage3Review.MaxContextTokens, chunkReviewer)

	return &Stage3{
		cfg:                cfg,
		mcpClient:          mcpClient,
		llm:                llm,
		promptLoader:       promptLoader,
		degradationManager: dm,
	}
}

// Review implements the Stage3Reviewer interface
func (s *Stage3) Review(ctx context.Context, req ReviewRequest, changes []FileChange, contextFiles []FileContent) (*domain.ReviewResult, error) {
	slog.Info("Stage 3: Starting Review (with Degradation Check)", "files_changed", len(changes), "context_files", len(contextFiles))

	// 1. Load Base Prompt (Empty Changes/Context) for token estimation
	baseData := map[string]interface{}{
		"PR":           req.PR,
		"ResultFormat": s.getResultFormat(),
		"Changes":      []FileChange{},
		"Context":      []FileContent{},
	}
	baseSystemPrompt, err := s.promptLoader.LoadPrompt(s.cfg.Stage3Review.PromptTemplate, baseData)
	if err != nil {
		return nil, fmt.Errorf("failed to load base prompt for estimation: %w", err)
	}

	// 2. Delegate to DegradationManager
	return s.degradationManager.ApplyStrategy(
		ctx, req, changes, contextFiles,
		s.cfg.Stage3Review.PromptTemplate,
		baseSystemPrompt,
		s.reviewCore,
	)
}

// reviewCore executes the actual LLM review
func (s *Stage3) reviewCore(ctx context.Context, req ReviewRequest, changes []FileChange, contextFiles []FileContent) (*domain.ReviewResult, error) {
	slog.Info("Stage 3: Executing Core Review", "files_changed", len(changes), "context_files", len(contextFiles))

	// 1. Prepare Prompt Data
	data := map[string]interface{}{
		"PR":           req.PR,
		"ResultFormat": s.getResultFormat(),
		"Changes":      changes,
		"Context":      contextFiles,
	}

	// 2. Load System Prompt
	// [New] Dynamic Language Rule Injection
	lRules, lNames := s.loadLanguageRules(changes)
	data["LanguageRules"] = lRules
	data["Language"] = lNames

	systemPromptStr, err := s.promptLoader.LoadPrompt(s.cfg.Stage3Review.PromptTemplate, data)
	if err != nil {
		return nil, fmt.Errorf("failed to load stage 3 prompt: %w", err)
	}

	// 3. User Message (can be simple, as system prompt contains everything)
	userMessage := fmt.Sprintf("Review PR %s: %s", req.PR.ID, req.PR.Title)

	// 4. Call LLM
	// Construct request using OpenAI types
	val := shared.NewResponseFormatJSONObjectParam()
	params := openai.ChatCompletionNewParams{
		Messages: []openai.ChatCompletionMessageParamUnion{
			openai.SystemMessage(systemPromptStr),
			openai.UserMessage(userMessage),
		},
		Temperature: openai.Float(s.cfg.Stage3Review.Temperature),
		ResponseFormat: openai.ChatCompletionNewParamsResponseFormatUnion{
			OfJSONObject: &val,
		},
	}

	resp, err := s.llm.Chat(ctx, params)
	if err != nil {
		return nil, fmt.Errorf("llm chat failed: %w", err)
	}

	if len(resp.Choices) == 0 {
		return nil, fmt.Errorf("received empty response from LLM")
	}

	responseStr := resp.Choices[0].Message.Content

	// 5. Parse Result
	var result domain.ReviewResult

	// Try to clean up markdown code blocks if present (common with some models)
	jsonStr := cleanJSON(responseStr)

	if err := json.Unmarshal([]byte(jsonStr), &result); err != nil {
		slog.Error("failed to unmarshal review result", "error", err, "response", responseStr)
		// Don't fail completely, return empty result with error summary
		return &domain.ReviewResult{
			Summary: fmt.Sprintf("Failed to parse review result: %v", err),
			Score:   0,
		}, nil
	}

	// Enrich comments with file paths if missing
	for i := range result.Comments {
		if result.Comments[i].Severity == "" {
			result.Comments[i].Severity = domain.CommentSeverityInfo // Default
		}
	}

	slog.Info("Stage 3: Completed", "comments_generated", len(result.Comments))
	return &result, nil
}

func (s *Stage3) getResultFormat() string {
	return `{
  "comments": [
    {
      "path": "path/to/file.go",
      "line": 42,
      "message": "Comment text...",
      "severity": "INFO|WARNING|CRITICAL|NIT"
    }
  ],
  "score": 85,
  "summary": "Overall review summary..."
}`
}

// cleanJSON removes markdown code block markers if present
func cleanJSON(s string) string {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "```json") {
		s = strings.TrimPrefix(s, "```json")
		s = strings.TrimSuffix(s, "```")
	} else if strings.HasPrefix(s, "```") {
		s = strings.TrimPrefix(s, "```")
		s = strings.TrimSuffix(s, "```")
	}
	return strings.TrimSpace(s)
}

// ----------------------------------------------------------------------------
// Dynamic Rule Detection Logic
// ----------------------------------------------------------------------------

func (s *Stage3) loadLanguageRules(changes []FileChange) (string, string) {
	detector := NewRuleDetector()
	rules := detector.Detect(changes)

	if len(rules) == 0 {
		return "", ""
	}

	var sb strings.Builder
	sb.WriteString("## Domain Specific Rules\n\n")

	for _, r := range rules {
		// Try to load prompts/rules/<rule>.md
		// We use PromptLoader.LoadPrompt but path is "rules/<rule>"
		content, err := s.promptLoader.LoadPrompt(filepath.Join("rules", r), nil)
		if err != nil {
			slog.Debug("rule prompt not found", "rule", r, "error", err)
			continue
		}
		sb.WriteString(content)
		sb.WriteString("\n\n")
	}

	return sb.String(), strings.Join(rules, ", ")
}

type RuleDetector struct {
	ExtRules      map[string]string
	FilenameRules map[string]string
	ContentRules  map[string]*regexp.Regexp
}

func NewRuleDetector() *RuleDetector {
	return &RuleDetector{
		ExtRules: map[string]string{
			".cpp": "cpp", ".cxx": "cpp", ".cc": "cpp", ".c": "cpp",
			".h": "cpp", ".hpp": "cpp", ".hxx": "cpp", ".inc": "cpp",
			".go": "go",
			".py": "py", ".pyi": "py", ".pyw": "py",
			".sql":  "sql",
			".java": "java",
		},
		FilenameRules: map[string]string{
			"Dockerfile": "docker",
		},
		ContentRules: map[string]*regexp.Regexp{
			"sql": regexp.MustCompile(`(?i)(SELECT\s+.+\s+FROM|INSERT\s+INTO|UPDATE\s+.+\s+SET|CREATE\s+TABLE|DELETE\s+FROM)`),
			"k8s": regexp.MustCompile(`(?i)^[\+\s]*(apiVersion:|kind:\s+(Deployment|Service|Pod|ConfigMap|Secret|Ingress|StatefulSet|DaemonSet|Job|CronJob))`),
		},
	}
}

func (d *RuleDetector) Detect(changes []FileChange) []string {
	detected := make(map[string]bool)

	for _, file := range changes {
		baseName := filepath.Base(file.Path)
		ext := strings.ToLower(filepath.Ext(file.Path))

		// 1. Filename Match (Prefix)
		for prefix, rule := range d.FilenameRules {
			if strings.HasPrefix(baseName, prefix) {
				detected[rule] = true
			}
		}

		// 2. Extension Match
		if rule, ok := d.ExtRules[ext]; ok {
			detected[rule] = true
		}

		// 3. Content Scan (Heuristic)
		// Only scan added lines
		for rule, pattern := range d.ContentRules {
			if detected[rule] {
				continue // Already detected
			}
			for _, hunk := range file.HunkLines {
				if strings.HasPrefix(hunk, "+") {
					// Check content
					if pattern.MatchString(hunk) {
						detected[rule] = true
						break // Found for this rule in this file
					}
				}
			}
		}
	}

	keys := make([]string, 0, len(detected))
	for k := range detected {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
