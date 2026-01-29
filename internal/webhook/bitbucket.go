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
	"sync" // Standard sync
	"time"
	"unicode/utf8"

	"pr-review-automation/internal/config"
	"pr-review-automation/internal/metrics"
	"pr-review-automation/internal/processor"
	internal_sync "pr-review-automation/internal/sync" // Custom sync package

	"github.com/tidwall/gjson"
)

// BitbucketWebhookHandler handles incoming Bitbucket webhook events
type BitbucketWebhookHandler struct {
	prProcessor    processor.Processor
	config         *config.Config
	parser         *PayloadParser
	workerPool     *WorkerPool
	debouncer      *internal_sync.Debouncer
	keyLock        *internal_sync.KeyLock
	latestPayloads sync.Map // Map[string][]byte: PR-ID -> Latest Payload
}

// NewBitbucketWebhookHandler creates a new webhook handler
func NewBitbucketWebhookHandler(cfg *config.Config, prProcessor processor.Processor, parser *PayloadParser) *BitbucketWebhookHandler {
	// Initialize Worker Pool
	queueSize := cfg.Server.QueueSize
	if queueSize <= 0 {
		queueSize = 100 // Safe default
	}
	workerCount := int(cfg.Server.ConcurrencyLimit)
	if workerCount <= 0 {
		workerCount = 1
	}

	wp := NewWorkerPool(workerCount, queueSize)
	wp.Start()

	// Initialize Debouncer
	debounceWindow := cfg.Server.DebounceWindow
	if debounceWindow <= 0 {
		debounceWindow = 2 * time.Second
	}
	debouncer := internal_sync.NewDebouncer(debounceWindow)
	keyLock := internal_sync.NewKeyLock()

	return &BitbucketWebhookHandler{
		prProcessor: prProcessor,
		config:      cfg,
		parser:      parser,
		workerPool:  wp,
		debouncer:   debouncer,
		keyLock:     keyLock,
	}
}

// WaitForCompletion blocks until all background PR processing tasks complete
func (h *BitbucketWebhookHandler) WaitForCompletion() {
	h.workerPool.Stop()
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

	metrics.WebhookRequests.WithLabelValues("accepted").Inc()

	// 3. Extract PR ID for Debouncing/Queueing
	// We do a quick parse or GJSON lookup to get the ID/EventKey without full parsing
	eventKey := gjson.GetBytes(body, "eventKey").String()
	// Only process specific events
	if eventKey != "pr:opened" && eventKey != "pr:from_ref_updated" {
		slog.Debug("ignoring event type for processing", "event_key", eventKey)
		// We still return 200 as we accepted the hook
		w.WriteHeader(http.StatusOK)
		fmt.Fprintln(w, "Event ignored")
		metrics.WebhookRequests.WithLabelValues("ignored_event").Inc()
		return
	}

	// Extract project/repo/id to form a unique key
	// Structure varies, but usually `pullRequest.id`
	// Extract project/repo/id to form a unique key
	// Structure varies, but usually `pullRequest.id`
	prID := gjson.GetBytes(body, "pullRequest.id").String()
	projectKey := gjson.GetBytes(body, "pullRequest.fromRef.repository.project.key").String()
	repoSlug := gjson.GetBytes(body, "pullRequest.fromRef.repository.slug").String()

	var uniqueKey string
	if prID != "" && projectKey != "" && repoSlug != "" {
		uniqueKey = fmt.Sprintf("%s/%s/%s", projectKey, repoSlug, prID)
	} else {
		// Fallback for L2/Unknown structures: Use ephemeral key
		// We can't debounce effectively but we preserve L2 fallback capability
		slog.Warn("could not extract pr identity, processing without specific lock", "pr_id", prID)
		uniqueKey = fmt.Sprintf("unknown-%d", time.Now().UnixNano())
	}

	// 4. Update the latest payload for this PR
	h.latestPayloads.Store(uniqueKey, body)

	// 5. Schedule via Debouncer
	h.debouncer.Add(uniqueKey, func() {
		h.submitJob(uniqueKey)
	})

	// Always return 200 OK immediately to Bitbucket
	w.WriteHeader(http.StatusOK)
	fmt.Fprintln(w, "Pull request queued for review")
}

func (h *BitbucketWebhookHandler) submitJob(uniqueKey string) {
	// 1. Retrieve Payload
	val, ok := h.latestPayloads.Load(uniqueKey) // Don't Delete yet, wait until processed? No, Load is fine.
	// Actually LoadAndDelete might be safer to ensure we process exactly what we have?
	// But if a new one comes in *while* we are submitting?
	// Let's LoadAndDelete. If a new one comes, it re-adds to map and schedules debouncer.
	// Wait, Check Debouncer logic:
	// If Add is called, it cancels previous timer.
	// But here the timer has fired.
	// So we LoadAndDelete.
	val, ok = h.latestPayloads.LoadAndDelete(uniqueKey)
	if !ok {
		return
	}
	payload := val.([]byte)

	// 2. Submit to WorkerPool
	err := h.workerPool.Submit(func(ctx context.Context) error {
		// Acquire PR-level Lock to ensure serial processing for this PR
		// This protects against multiple workers picking up different debounced events for same PR (rare but possible)
		h.keyLock.Lock(uniqueKey)
		defer h.keyLock.Unlock(uniqueKey)

		// Panic recovery for safety
		defer func() {
			if r := recover(); r != nil {
				slog.Error("Panic recovered in pr worker", "panic", r, "stack", string(debug.Stack()))
			}
		}()

		// Full Parse inside worker
		// Calculate timeout for actual processing
		procCtx, cancel := context.WithTimeout(ctx, 15*time.Minute)
		defer cancel()

		pr, err := h.parser.Parse(procCtx, payload)
		if err != nil {
			slog.Error("payload parse failed", "error", err)
			metrics.PayloadParseFailures.WithLabelValues("both").Inc()
			return err
		}

		if !pr.IsValid() {
			slog.Error("parsed pr invalid", "pr", pr)
			metrics.WebhookRequests.WithLabelValues("invalid_payload").Inc()
			return fmt.Errorf("invalid pr")
		}

		slog.Info("processing pr", "pr_id", pr.ID, "repo", pr.RepoSlug)
		if err := h.prProcessor.ProcessPullRequest(procCtx, pr); err != nil {
			slog.Error("process pr failed", "error", err, "pr_id", pr.ID)
			return err
		}
		return nil
	})

	if err != nil {
		if err == ErrQueueFull {
			slog.Warn("worker pool queue full, dropping request", "pr", uniqueKey)
			metrics.WebhookRequests.WithLabelValues("dropped_full").Inc()
			// We can't return 429 here because this is async.
			// Ideally we would return 429 in ServeHTTP if we checked queue size there.
			// Implementing "Fail Fast" in ServeHTTP:
			// len(p.Queue) == cap(p.Queue) -> return 429.
			// But since we debounce, we might not know if queue is full until later.
			// However, dropping here is the fallback safety.
		} else {
			slog.Error("submit job failed", "error", err)
		}
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
