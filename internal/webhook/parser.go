package webhook

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"pr-review-automation/internal/agent"
	"pr-review-automation/internal/config"
	"pr-review-automation/internal/domain"
	"pr-review-automation/internal/filter"
	"pr-review-automation/internal/metrics"
	"pr-review-automation/internal/types"

	"github.com/tidwall/gjson"
	"google.golang.org/adk/model"
)

// TextQuerier is an interface that allows simple text-based interaction with an LLM.
// This is used to assert the capability of the provided model.LLM.
type TextQuerier interface {
	SimpleTextQuery(ctx context.Context, systemPrompt, userInput string) (string, error)
}

// PayloadParser is responsible for extracting PullRequest domain models from raw JSON payloads.
// It employs a two-layer strategy:
// L1: Fast, rule-based probing using gjson.
// L2: Robust, LLM-based extraction for unknown structures.
type PayloadParser struct {
	cfg           config.WebhookConfig
	llm           model.LLM
	promptLoader  *agent.PromptLoader
	payloadFilter filter.PayloadFilter
}

// NewPayloadParser creates a new PayloadParser.
func NewPayloadParser(cfg config.WebhookConfig, llm model.LLM, promptLoader *agent.PromptLoader, payloadFilter filter.PayloadFilter) *PayloadParser {
	return &PayloadParser{
		cfg:           cfg,
		llm:           llm,
		promptLoader:  promptLoader,
		payloadFilter: payloadFilter,
	}
}

// Parse attempts to parse the webhook payload into a domain.PullRequest.
// It first tries the fast path (L1), and falls back to the slow path (L2) if necessary.
func (p *PayloadParser) Parse(ctx context.Context, body []byte) (*domain.PullRequest, error) {
	// Phase 1: gjson probing (L1)
	pr := p.probePayload(body)
	if pr.IsValid() {
		return pr, nil
	}

	// Phase 2: LLM Fallback (L2)
	slog.Warn("L1 probing failed, attempting L2 LLM fallback")
	return p.askLLMToExtract(ctx, body)
}

// probePayload implements the L1 parsing strategy using gjson paths.
func (p *PayloadParser) probePayload(body []byte) *domain.PullRequest {
	if !gjson.ValidBytes(body) {
		return &domain.PullRequest{}
	}

	// Define candidate paths for each field, prioritized from left to right.
	pathsProjectKey := []string{
		"pullRequest.toRef.repository.project.key",   // Bitbucket Server (New)
		"repository.project.key",                     // Bitbucket Cloud / Old Server
		"pullRequest.fromRef.repository.project.key", // Fallback
		"project.key", // Flattened
	}

	pathsRepoSlug := []string{
		"pullRequest.toRef.repository.slug",
		"repository.slug",
		"repository.name",
		"pullRequest.fromRef.repository.slug",
	}

	pathsID := []string{
		"pullRequest.id",
		"id",
	}

	pathsTitle := []string{
		"pullRequest.title",
		"title",
	}

	pathsDesc := []string{
		"pullRequest.description",
		"description",
	}

	pathsAuthor := []string{
		"pullRequest.author.user.displayName", // Complex struct
		"pullRequest.author.user.name",
		"pullRequest.author.displayName",
		"pullRequest.author.name", // Flat struct
		"actor.displayName",
		"actor.name",
	}

	// Helper to probe first valid string result
	probeString := func(paths []string) string {
		return probe(body, paths).String()
	}

	// Helper to probe ID which might be int or string
	probeID := func(paths []string) string {
		res := probe(body, paths)
		if res.Exists() {
			return res.String()
		}
		return ""
	}

	// Paths for latestCommit (from source branch)
	pathsLatestCommit := []string{
		"pullRequest.fromRef.latestCommit",
		"fromRef.latestCommit",
	}

	return &domain.PullRequest{
		ID:           probeID(pathsID),
		ProjectKey:   probeString(pathsProjectKey),
		RepoSlug:     probeString(pathsRepoSlug),
		Title:        probeString(pathsTitle),
		Description:  probeString(pathsDesc),
		Author:       probeString(pathsAuthor),
		LatestCommit: probeString(pathsLatestCommit),
	}
}

func probe(body []byte, paths []string) gjson.Result {
	for _, path := range paths {
		res := gjson.GetBytes(body, path)
		if res.Exists() && res.Value() != nil {
			return res
		}
	}
	return gjson.Result{}
}

// askLLMToExtract implements the L2 parsing strategy using LLM.
func (p *PayloadParser) askLLMToExtract(ctx context.Context, body []byte) (*domain.PullRequest, error) {
	// 1. Prepare Prompt
	sysPrompt, err := p.promptLoader.LoadPrompt("system/pr_webhook_parser")
	if err != nil {
		// Fallback prompt if loader fails
		sysPrompt = "You are a JSON parser. Extract id, projectKey, repoSlug, title, description, authorName as JSON."
		slog.Warn("load prompt failed, using fallback", "error", err)
	}

	// 2. Truncate Body
	truncated := p.truncateForLLM(body)

	// 3. Assert Interface
	querier, ok := p.llm.(TextQuerier)
	if !ok {
		return nil, fmt.Errorf("llm does not support simple text query")
	}

	// 4. Retry Logic
	maxRetries := p.cfg.MaxRetries
	if maxRetries <= 0 {
		maxRetries = 2
	}
	var lastErr error

	for attempt := 0; attempt <= maxRetries; attempt++ {
		if attempt > 0 {
			backoff := time.Duration(attempt) * time.Second
			select {
			case <-time.After(backoff):
			case <-ctx.Done():
				return nil, ctx.Err()
			}
			slog.Warn("retrying llm call", "attempt", attempt+1, "max", maxRetries+1)
		}

		// Execute LLM Call
		respText, err := querier.SimpleTextQuery(ctx, sysPrompt, truncated)
		if err == nil {
			// Clean up response (sometimes LLMs include markdown blocks)
			respText = types.CleanJSONFromMarkdown(respText)

			var pr domain.PullRequest
			if err := json.Unmarshal([]byte(respText), &pr); err != nil {
				lastErr = fmt.Errorf("unmarshal llm response: %w", err)
				continue // Retry on malformed JSON
			}
			return &pr, nil
		}

		lastErr = err
		slog.Warn("llm call failed", "attempt", attempt+1, "error", err)

		if !p.isRetryableError(err) {
			slog.Error("llm permanent error", "error", err)
			break
		}
	}

	metrics.PayloadParseFailures.WithLabelValues("l2").Inc()
	return nil, fmt.Errorf("l2 extraction failed: %w", lastErr)
}

func (p *PayloadParser) truncateForLLM(body []byte) string {
	if !gjson.ValidBytes(body) {
		// If invalid JSON, just return string
		return string(body)
	}

	if p.payloadFilter != nil {
		filtered := p.payloadFilter.Filter(body)
		return string(filtered)
	}

	return string(body)
}

func (p *PayloadParser) isRetryableError(err error) bool {
	if err == nil {
		return false
	}

	// Check for explicit RetryableError (from adapter)
	var retryErr *types.RetryableError
	if errors.As(err, &retryErr) {
		return true
	}

	// Check for standard context errors
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return true
	}

	return false
}
