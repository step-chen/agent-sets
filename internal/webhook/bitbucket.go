package webhook

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"pr-review-automation/internal/config"
	"pr-review-automation/internal/domain"
	"pr-review-automation/internal/metrics"
	"pr-review-automation/internal/processor"
)

// BitbucketWebhookHandler handles incoming Bitbucket webhook events
type BitbucketWebhookHandler struct {
	prProcessor processor.Processor
	config      *config.Config
	sem         chan struct{} // Semaphore to limit concurrent processing
	wg          sync.WaitGroup
}

// NewBitbucketWebhookHandler creates a new webhook handler
func NewBitbucketWebhookHandler(cfg *config.Config, prProcessor processor.Processor) *BitbucketWebhookHandler {
	return &BitbucketWebhookHandler{
		prProcessor: prProcessor,
		config:      cfg,
		sem:         make(chan struct{}, cfg.Server.ConcurrencyLimit),
	}
}

// WaitForCompletion blocks until all background PR processing tasks complete
func (h *BitbucketWebhookHandler) WaitForCompletion() {
	h.wg.Wait()
}

// BitbucketWebhookPayload represents the structure of a Bitbucket webhook payload
type BitbucketWebhookPayload struct {
	EventKey   string `json:"eventKey"`
	Repository struct {
		Name    string `json:"name"`
		Slug    string `json:"slug"`
		Project struct {
			Key string `json:"key"`
		} `json:"project"`
	} `json:"repository"`
	PullRequest struct {
		ID          int    `json:"id"`
		Title       string `json:"title"`
		Description string `json:"description"`
		Author      struct {
			Name string `json:"name"`
		} `json:"author"`
	} `json:"pullRequest"`
}

// ServeHTTP handles incoming webhook requests
func (h *BitbucketWebhookHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	slog.Debug("Received webhook request", "method", r.Method, "content_length", r.ContentLength)
	metrics.WebhookRequests.WithLabelValues("received").Inc()

	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// 1. Security: Limit request body size
	r.Body = http.MaxBytesReader(w, r.Body, h.config.Server.MaxBodySize)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		slog.Warn("read body failed", "error", err)
		http.Error(w, "Error reading request body", http.StatusBadRequest)
		metrics.WebhookRequests.WithLabelValues("error_read").Inc()
		return
	}

	// 2. Security: Verify webhook signature if secret is configured
	if h.config.Server.WebhookSecret != "" {
		signature := r.Header.Get("X-Hub-Signature")
		if signature == "" {
			slog.Warn("missing signature")
			http.Error(w, "Missing signature", http.StatusUnauthorized)
			metrics.WebhookRequests.WithLabelValues("invalid_signature").Inc()
			return
		}

		if !verifySignature(body, signature, h.config.Server.WebhookSecret) {
			slog.Warn("invalid signature")
			http.Error(w, "Invalid signature", http.StatusUnauthorized)
			metrics.WebhookRequests.WithLabelValues("invalid_signature").Inc()
			return
		}

	}

	var payload BitbucketWebhookPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		slog.Warn("parse payload failed", "error", err)
		http.Error(w, "Invalid JSON payload", http.StatusBadRequest)
		metrics.WebhookRequests.WithLabelValues("invalid_json").Inc()
		return
	}

	slog.Debug("Parsed webhook payload",
		"event_key", payload.EventKey,
		"project", payload.Repository.Project.Key,
		"repo", payload.Repository.Slug,
		"pr_id", payload.PullRequest.ID,
		"pr_title", payload.PullRequest.Title,
	)

	// Only process pull request opened or updated events
	if payload.EventKey != "pr:opened" && payload.EventKey != "pr:updated" {
		slog.Debug("Ignoring webhook event", "event_key", payload.EventKey)
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, "Event %s ignored", payload.EventKey)
		metrics.WebhookRequests.WithLabelValues("ignored").Inc()
		return
	}

	metrics.WebhookRequests.WithLabelValues("accepted").Inc()

	// 2. Concurrency: Check capacity BEFORE creating goroutine to prevent goroutine leak
	select {
	case h.sem <- struct{}{}:
		// Acquired semaphore, proceed with async processing
		h.wg.Add(1)
		go func() {
			defer h.wg.Done()
			defer func() { <-h.sem }()

			// Panic recovery to prevent goroutine crash
			defer func() {
				if r := recover(); r != nil {
					slog.Error("Panic recovered in webhook handler",
						"panic", r,
						"stack", string(debug.Stack()))
				}
			}()

			// Nil check for processor
			if h.prProcessor == nil {
				slog.Error("processor is nil")
				return
			}

			// Use context with timeout
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
			defer cancel()

			// Convert to Domain Model
			pr := &domain.PullRequest{
				ID:          fmt.Sprintf("%d", payload.PullRequest.ID),
				ProjectKey:  payload.Repository.Project.Key,
				RepoSlug:    payload.Repository.Slug,
				Title:       payload.PullRequest.Title,
				Description: payload.PullRequest.Description,
				Author:      payload.PullRequest.Author.Name,
			}

			slog.Info("processing pr", "pr_id", pr.ID, "repo", pr.RepoSlug)

			// Process the PR
			if err := h.prProcessor.ProcessPullRequest(ctx, pr); err != nil {
				slog.Error("process pr failed", "error", err, "pr_id", pr.ID)
			}
		}()

		w.WriteHeader(http.StatusOK)
		fmt.Fprintln(w, "Pull request queued for review")

	default:
		// At capacity, reject with 429 Too Many Requests
		slog.Warn("concurrency limit, request dropped",
			"pr_id", payload.PullRequest.ID,
			"repo", payload.Repository.Slug)
		metrics.WebhookRequests.WithLabelValues("dropped_concurrency").Inc()
		http.Error(w, "Server busy, please retry later", http.StatusTooManyRequests)
	}
}

// verifySignature validates the HMAC-SHA256 signature of a webhook request
// Expected header format: sha256=<hex-encoded-signature>
func verifySignature(body []byte, signature, secret string) bool {
	// Bitbucket uses sha256=<signature> format
	parts := strings.SplitN(signature, "=", 2)
	if len(parts) != 2 {
		return false
	}

	algorithm := parts[0]
	providedSig := parts[1]

	// Only support SHA256
	if algorithm != "sha256" {
		slog.Warn("Unsupported signature algorithm", "algorithm", algorithm)
		return false
	}

	// Compute expected signature
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	expectedSig := hex.EncodeToString(mac.Sum(nil))

	// Use constant-time comparison to prevent timing attacks
	return hmac.Equal([]byte(expectedSig), []byte(providedSig))
}
