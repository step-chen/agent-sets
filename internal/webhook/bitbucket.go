package webhook

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"runtime/debug"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"pr-review-automation/internal/config"
	"pr-review-automation/internal/metrics"
	"pr-review-automation/internal/processor"

	"github.com/tidwall/gjson"
)

// BitbucketWebhookHandler handles incoming Bitbucket webhook events
type BitbucketWebhookHandler struct {
	prProcessor processor.Processor
	config      *config.Config
	parser      *PayloadParser
	sem         chan struct{} // Semaphore to limit concurrent processing
	wg          sync.WaitGroup
}

// NewBitbucketWebhookHandler creates a new webhook handler
func NewBitbucketWebhookHandler(cfg *config.Config, prProcessor processor.Processor, parser *PayloadParser) *BitbucketWebhookHandler {
	return &BitbucketWebhookHandler{
		prProcessor: prProcessor,
		config:      cfg,
		parser:      parser,
		sem:         make(chan struct{}, cfg.Server.ConcurrencyLimit),
	}
}

// WaitForCompletion blocks until all background PR processing tasks complete
func (h *BitbucketWebhookHandler) WaitForCompletion() {
	h.wg.Wait()
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

	// Check if body is valid UTF-8
	if !utf8.Valid(body) {
		slog.Warn("request body is not valid utf-8")
		http.Error(w, "Invalid encoding", http.StatusBadRequest)
		metrics.WebhookRequests.WithLabelValues("invalid_encoding").Inc()
		return
	}

	// Note: We delay parsing to the async goroutine to return 200 OK quickly.
	// However, we could do a quick L1 probe here?
	// The requirement implies asynchronous processing.
	// But validation failure should ideally be user-visible?
	// Given the instructions ("async scenario... cannot return 500"), we stick to full async.

	metrics.WebhookRequests.WithLabelValues("accepted").Inc()

	// 3. Concurrency: Check capacity BEFORE creating goroutine to prevent goroutine leak
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

			// Use context with timeout - Increased to 15m to handle up to 10 tool calls + LLM latency
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
			defer cancel()

			// Parse Payload using Robust Parser
			pr, err := h.parser.Parse(ctx, body)
			if err != nil {
				slog.Error("payload parse failed",
					"error", err,
					"payload_preview", truncateForLog(body, 500),
				)
				metrics.PayloadParseFailures.WithLabelValues("both").Inc()
				return
			}

			// Event Key Check
			eventKey := gjson.GetBytes(body, "eventKey").String()
			// Only process specific events
			// pr:opened - New PR
			// pr:from_ref_updated - Source branch updated (new commits)
			if eventKey != "pr:opened" && eventKey != "pr:from_ref_updated" {
				slog.Info("ignoring event", "event_key", eventKey, "pr_id", pr.ID)
				metrics.WebhookRequests.WithLabelValues("ignored_event").Inc()
				return
			}

			if !pr.IsValid() {
				slog.Error("parsed pr invalid (missing key fields)", "pr", pr)
				metrics.WebhookRequests.WithLabelValues("invalid_payload").Inc()
				return
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
		slog.Warn("concurrency limit, request dropped")
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

func truncateForLog(b []byte, max int) string {
	if len(b) > max {
		return string(b[:max]) + "..."
	}
	return string(b)
}
