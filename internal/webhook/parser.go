package webhook

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"pr-review-automation/internal/agent"
	"pr-review-automation/internal/domain"
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
	llm          model.LLM
	promptLoader *agent.PromptLoader
}

// NewPayloadParser creates a new PayloadParser.
func NewPayloadParser(llm model.LLM, promptLoader *agent.PromptLoader) *PayloadParser {
	return &PayloadParser{
		llm:          llm,
		promptLoader: promptLoader,
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

	return &domain.PullRequest{
		ID:          probeID(pathsID),
		ProjectKey:  probeString(pathsProjectKey),
		RepoSlug:    probeString(pathsRepoSlug),
		Title:       probeString(pathsTitle),
		Description: probeString(pathsDesc),
		Author:      probeString(pathsAuthor),
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
	const maxRetries = 2
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
		// If invalid JSON, just return string, maybe truncated
		return string(body)
	}

	// We want to preserve structure but prune heavy fields.
	// Since modifying JSON structure robustly in Go without struct is hard,
	// we use a simpler strategy:
	// Parse into map[string]interface{}, prune known heavy fields, re-marshal.

	var data map[string]interface{}
	if err := json.Unmarshal(body, &data); err != nil {
		return string(body)
	}

	// Recursive prune
	prune(data, 0)

	res, _ := json.Marshal(data)
	return string(res)
}

func prune(v interface{}, depth int) {
	if depth > 10 {
		return
	}

	switch val := v.(type) {
	case map[string]interface{}:
		for k, v2 := range val {
			// Prune rule 1: Remove specific heavy keys
			if k == "reviewers" || k == "participants" || k == "commits" || k == "diff" || k == "links" {
				delete(val, k)
				continue
			}
			// Prune rule 2: Truncate long strings (description, summary)
			if strVal, ok := v2.(string); ok {
				if (k == "description" || k == "summary" || k == "body") && len(strVal) > 500 {
					val[k] = strVal[:500] + "...(truncated)"
					continue
				}
			}
			prune(v2, depth+1)
		}
	case []interface{}:
		// Prune rule 3: Truncate arrays to max 1 item (to keep structure sample)
		// But don't touch them if they are small objects, only if list of objects
		// Actually, for webhook, most arrays like 'reviewers' are redundant for parsing ID/Repo.
		// However, protecting 'toRef'/'fromRef' which are objects, not arrays.
		// If validation errors occurs, maybe we trimmed something important?
		// We'll iterate items.
		for _, item := range val {
			prune(item, depth+1)
		}
	}
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
