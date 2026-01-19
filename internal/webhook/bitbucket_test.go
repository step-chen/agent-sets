package webhook

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"pr-review-automation/internal/config"
	"pr-review-automation/internal/domain"
)

// MockProcessor implements processor.Processor for testing
type MockProcessor struct {
	ProcessFunc func(ctx context.Context, pr *domain.PullRequest) error
}

func (m *MockProcessor) ProcessPullRequest(ctx context.Context, pr *domain.PullRequest) error {
	if m.ProcessFunc != nil {
		return m.ProcessFunc(ctx, pr)
	}
	return nil
}

func TestBitbucketWebhookHandler_MethodNotAllowed(t *testing.T) {
	cfg := &config.Config{
		Server: struct {
			Port             int           `yaml:"port"`
			ConcurrencyLimit int64         `yaml:"concurrency_limit"`
			ReadTimeout      time.Duration `yaml:"read_timeout"`
			WriteTimeout     time.Duration `yaml:"write_timeout"`
			MaxBodySize      int64         `yaml:"max_body_size"`
			WebhookSecret    string        `yaml:"-"` // From Env
		}{
			MaxBodySize:      2 * 1024 * 1024,
			ConcurrencyLimit: 10,
		},
	}
	handler := NewBitbucketWebhookHandler(cfg, nil)

	req := httptest.NewRequest(http.MethodGet, "/webhook", nil)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected status %d, got %d", http.StatusMethodNotAllowed, w.Code)
	}
}

func TestBitbucketWebhookHandler_InvalidJSON(t *testing.T) {
	cfg := &config.Config{
		Server: struct {
			Port             int           `yaml:"port"`
			ConcurrencyLimit int64         `yaml:"concurrency_limit"`
			ReadTimeout      time.Duration `yaml:"read_timeout"`
			WriteTimeout     time.Duration `yaml:"write_timeout"`
			MaxBodySize      int64         `yaml:"max_body_size"`
			WebhookSecret    string        `yaml:"-"` // From Env
		}{
			MaxBodySize:      2 * 1024 * 1024,
			ConcurrencyLimit: 10,
		},
	}
	handler := NewBitbucketWebhookHandler(cfg, nil)

	req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewBufferString("not valid json"))
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected status %d, got %d", http.StatusBadRequest, w.Code)
	}
}

func TestBitbucketWebhookHandler_IgnoredEvent(t *testing.T) {
	cfg := &config.Config{
		Server: struct {
			Port             int           `yaml:"port"`
			ConcurrencyLimit int64         `yaml:"concurrency_limit"`
			ReadTimeout      time.Duration `yaml:"read_timeout"`
			WriteTimeout     time.Duration `yaml:"write_timeout"`
			MaxBodySize      int64         `yaml:"max_body_size"`
			WebhookSecret    string        `yaml:"-"` // From Env
		}{
			MaxBodySize:      2 * 1024 * 1024,
			ConcurrencyLimit: 10,
		},
	}
	handler := NewBitbucketWebhookHandler(cfg, nil)

	payload := BitbucketWebhookPayload{
		EventKey: "repo:push", // Not a PR event
	}
	body, _ := json.Marshal(payload)

	req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewBuffer(body))
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status %d, got %d", http.StatusOK, w.Code)
	}

	if !bytes.Contains(w.Body.Bytes(), []byte("ignored")) {
		t.Errorf("expected response to contain 'ignored', got %s", w.Body.String())
	}
}

func TestBitbucketWebhookHandler_PROpenedEvent(t *testing.T) {
	cfg := &config.Config{
		Server: struct {
			Port             int           `yaml:"port"`
			ConcurrencyLimit int64         `yaml:"concurrency_limit"`
			ReadTimeout      time.Duration `yaml:"read_timeout"`
			WriteTimeout     time.Duration `yaml:"write_timeout"`
			MaxBodySize      int64         `yaml:"max_body_size"`
			WebhookSecret    string        `yaml:"-"` // From Env
		}{
			MaxBodySize:      2 * 1024 * 1024,
			ConcurrencyLimit: 10,
		},
	}
	// Mock processor
	mockProc := &MockProcessor{
		ProcessFunc: func(ctx context.Context, pr *domain.PullRequest) error {
			return nil
		},
	}
	handler := NewBitbucketWebhookHandler(cfg, mockProc)

	payload := BitbucketWebhookPayload{
		EventKey: "pr:opened",
	}
	payload.Repository.Name = "test-repo"
	payload.Repository.Slug = "test-repo"
	payload.Repository.Project.Key = "TEST"
	payload.PullRequest.ID = 123
	payload.PullRequest.Title = "Test PR"
	payload.PullRequest.Description = "Test description"
	payload.PullRequest.Author.Name = "testuser"

	body, _ := json.Marshal(payload)

	req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewBuffer(body))
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status %d, got %d", http.StatusOK, w.Code)
	}

	if !bytes.Contains(w.Body.Bytes(), []byte("queued")) {
		t.Errorf("expected response to contain 'queued', got %s", w.Body.String())
	}

	// Wait for async processing
	time.Sleep(50 * time.Millisecond)
}

func TestBitbucketWebhookHandler_BodySizeLimit(t *testing.T) {
	cfg := &config.Config{
		Server: struct {
			Port             int           `yaml:"port"`
			ConcurrencyLimit int64         `yaml:"concurrency_limit"`
			ReadTimeout      time.Duration `yaml:"read_timeout"`
			WriteTimeout     time.Duration `yaml:"write_timeout"`
			MaxBodySize      int64         `yaml:"max_body_size"`
			WebhookSecret    string        `yaml:"-"` // From Env
		}{
			MaxBodySize:      10, // Very small limit
			ConcurrencyLimit: 10,
		},
	}
	handler := NewBitbucketWebhookHandler(cfg, nil)

	// Create a payload larger than the limit
	largePayload := bytes.Repeat([]byte("a"), 100)

	req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewBuffer(largePayload))
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected status %d, got %d", http.StatusBadRequest, w.Code)
	}
}

func TestVerifySignature_Valid(t *testing.T) {
	body := []byte(`{"test": "data"}`)
	secret := "my-secret-key"

	// Compute expected signature
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	expectedSig := "sha256=" + hex.EncodeToString(mac.Sum(nil))

	if !verifySignature(body, expectedSig, secret) {
		t.Error("expected signature to be valid")
	}
}

func TestVerifySignature_Invalid(t *testing.T) {
	body := []byte(`{"test": "data"}`)
	secret := "my-secret-key"

	if verifySignature(body, "sha256=invalid", secret) {
		t.Error("expected signature to be invalid")
	}
}

func TestVerifySignature_MissingPrefix(t *testing.T) {
	body := []byte(`{"test": "data"}`)
	secret := "my-secret-key"

	if verifySignature(body, "invalid-no-prefix", secret) {
		t.Error("expected signature without prefix to be invalid")
	}
}

func TestVerifySignature_WrongAlgorithm(t *testing.T) {
	body := []byte(`{"test": "data"}`)
	secret := "my-secret-key"

	if verifySignature(body, "sha1=somesignature", secret) {
		t.Error("expected wrong algorithm to be rejected")
	}
}
