package webhook

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"pr-review-automation/internal/config"
	"pr-review-automation/internal/domain"
	"pr-review-automation/internal/pipeline"

	"github.com/openai/openai-go"
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

// MockLLM implements llm.Client for testing
type MockLLM struct {
	SimpleQueryFunc func(ctx context.Context, prompt, input string) (string, error)
	ChatFunc        func(ctx context.Context, params openai.ChatCompletionNewParams) (*openai.ChatCompletion, error)
}

func (m *MockLLM) Chat(ctx context.Context, params openai.ChatCompletionNewParams) (*openai.ChatCompletion, error) {
	if m.ChatFunc != nil {
		return m.ChatFunc(ctx, params)
	}
	return &openai.ChatCompletion{}, nil
}

func (m *MockLLM) SimpleTextQuery(ctx context.Context, systemPrompt, userInput string) (string, error) {
	if m.SimpleQueryFunc != nil {
		return m.SimpleQueryFunc(ctx, systemPrompt, userInput)
	}
	return "{}", nil
}

// Helper to create a parser with mocked dependencies
func createTestParser(t *testing.T, llm *MockLLM) *PayloadParser {
	tmpDir := t.TempDir()
	// Create a dummy prompt file
	os.MkdirAll(filepath.Join(tmpDir, "system"), 0755)
	os.WriteFile(filepath.Join(tmpDir, "system/pr_webhook_parser.md"), []byte("dummy prompt"), 0644)

	loader := pipeline.NewPromptLoader(tmpDir)
	return NewPayloadParser(config.WebhookConfig{}, llm, loader, nil)
}

func TestBitbucketWebhookHandler_MethodNotAllowed(t *testing.T) {
	cfg := &config.Config{
		Server: struct {
			Port             int           `yaml:"port"`
			ConcurrencyLimit int64         `yaml:"concurrency_limit"`
			ReadTimeout      time.Duration `yaml:"read_timeout"`
			WriteTimeout     time.Duration `yaml:"write_timeout"`
			ShutdownTimeout  time.Duration `yaml:"shutdown_timeout"`
			MaxBodySize      int64         `yaml:"max_body_size"`
			WebhookSecret    string        `yaml:"-"`
		}{
			MaxBodySize:      2 * 1024 * 1024,
			ConcurrencyLimit: 10,
		},
	}
	parser := createTestParser(t, &MockLLM{})
	handler := NewBitbucketWebhookHandler(cfg, nil, parser)

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
			ShutdownTimeout  time.Duration `yaml:"shutdown_timeout"`
			MaxBodySize      int64         `yaml:"max_body_size"`
			WebhookSecret    string        `yaml:"-"`
		}{
			MaxBodySize:      2 * 1024 * 1024,
			ConcurrencyLimit: 10,
		},
	}
	parser := createTestParser(t, &MockLLM{})
	handler := NewBitbucketWebhookHandler(cfg, nil, parser)

	req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewBufferString("not valid json"))
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	// Verify extraction (simplified since we mock dependencies)
	// In a real test, we would verify if the aggregator was called with the right data
	// Actually, wait. The new ServeHTTP logic REMOVED the synchronous Unmarshal check.
	// It only checks UTF8.
	// So "not valid json" (if utf8) will be accepted (200 OK) and fail asynchronously.
	// But `not valid json` IS valid utf8.
	// So status should be 200 OK now! The parser is async.

	if w.Code != http.StatusOK {
		t.Errorf("expected status %d, got %d", http.StatusOK, w.Code)
	}
}

func TestBitbucketWebhookHandler_PROpenedEvent_L1(t *testing.T) {
	cfg := &config.Config{
		Server: struct {
			Port             int           `yaml:"port"`
			ConcurrencyLimit int64         `yaml:"concurrency_limit"`
			ReadTimeout      time.Duration `yaml:"read_timeout"`
			WriteTimeout     time.Duration `yaml:"write_timeout"`
			ShutdownTimeout  time.Duration `yaml:"shutdown_timeout"`
			MaxBodySize      int64         `yaml:"max_body_size"`
			WebhookSecret    string        `yaml:"-"`
		}{
			MaxBodySize:      2 * 1024 * 1024,
			ConcurrencyLimit: 10,
		},
	}

	processed := make(chan *domain.PullRequest, 1)
	mockProc := &MockProcessor{
		ProcessFunc: func(ctx context.Context, pr *domain.PullRequest) error {
			processed <- pr
			return nil
		},
	}

	parser := createTestParser(t, &MockLLM{})
	handler := NewBitbucketWebhookHandler(cfg, mockProc, parser)

	// L1 Payload
	jsonBody := `{
		"eventKey": "pr:opened",
		"pullRequest": {
			"id": 123,
			"title": "Test PR",
			"description": "Desc",
			"toRef": {
				"repository": {
					"slug": "my-repo",
					"project": { "key": "PROJ" }
				}
			},
			"author": {
				"user": { "name": "alice" }
			}
		}
	}`

	req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewBufferString(jsonBody))
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status %d, got %d", http.StatusOK, w.Code)
	}

	select {
	case pr := <-processed:
		if pr.ID != "123" {
			t.Errorf("expected ID 123, got %s", pr.ID)
		}
		if pr.RepoSlug != "my-repo" {
			t.Errorf("expected repo my-repo, got %s", pr.RepoSlug)
		}
	case <-time.After(1 * time.Second):
		t.Error("timeout waiting for processing")
	}
}

func TestBitbucketWebhookHandler_PROpenedEvent_L2(t *testing.T) {
	cfg := &config.Config{
		Server: struct {
			Port             int           `yaml:"port"`
			ConcurrencyLimit int64         `yaml:"concurrency_limit"`
			ReadTimeout      time.Duration `yaml:"read_timeout"`
			WriteTimeout     time.Duration `yaml:"write_timeout"`
			ShutdownTimeout  time.Duration `yaml:"shutdown_timeout"`
			MaxBodySize      int64         `yaml:"max_body_size"`
			WebhookSecret    string        `yaml:"-"`
		}{
			MaxBodySize:      2 * 1024 * 1024,
			ConcurrencyLimit: 10,
		},
	}

	processed := make(chan *domain.PullRequest, 1)
	mockProc := &MockProcessor{
		ProcessFunc: func(ctx context.Context, pr *domain.PullRequest) error {
			processed <- pr
			return nil
		},
	}

	mockLLM := &MockLLM{
		SimpleQueryFunc: func(ctx context.Context, prompt, input string) (string, error) {
			return `{
				"id": "999",
				"projectKey": "LLM_PROJ",
				"repoSlug": "llm-repo",
				"title": "Extracted by LLM",
				"description": "It works",
				"authorName": "ai-user"
			}`, nil
		},
	}

	parser := createTestParser(t, mockLLM)
	handler := NewBitbucketWebhookHandler(cfg, mockProc, parser)

	// Payload with completely unknown structure that L1 fails to parse all required fields
	// L1 needs ID, ProjectKey to consider "Valid" (actually IsValid checks ID, ProjectKey, RepoSlug)
	jsonBody := `{
		"eventKey": "pr:opened",
		"weirdEvent": "pr:weird",
		"data": {
			"meta": { "identifier": 999 },
			"details": { "about": "some stuff" }
		}
	}`

	req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewBufferString(jsonBody))
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status %d, got %d", http.StatusOK, w.Code)
	}

	select {
	case pr := <-processed:
		if pr.ID != "999" {
			t.Errorf("expected ID 999, got %s", pr.ID)
		}
		if pr.ProjectKey != "LLM_PROJ" {
			t.Errorf("expected ID LLM_PROJ, got %s", pr.ProjectKey)
		}
	case <-time.After(1 * time.Second):
		t.Error("timeout waiting for processing")
	}
}

func TestBitbucketWebhookHandler_BodySizeLimit(t *testing.T) {
	cfg := &config.Config{
		Server: struct {
			Port             int           `yaml:"port"`
			ConcurrencyLimit int64         `yaml:"concurrency_limit"`
			ReadTimeout      time.Duration `yaml:"read_timeout"`
			WriteTimeout     time.Duration `yaml:"write_timeout"`
			ShutdownTimeout  time.Duration `yaml:"shutdown_timeout"`
			MaxBodySize      int64         `yaml:"max_body_size"`
			WebhookSecret    string        `yaml:"-"`
		}{
			MaxBodySize:      10, // Very small limit
			ConcurrencyLimit: 10,
		},
	}
	parser := createTestParser(t, &MockLLM{})
	handler := NewBitbucketWebhookHandler(cfg, nil, parser)

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
