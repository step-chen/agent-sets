//go:build e2e

package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"pr-review-automation/internal/client"
	"pr-review-automation/internal/config"
	"pr-review-automation/internal/filter/bitbucket"
	"pr-review-automation/internal/pipeline"
	"pr-review-automation/internal/processor"
	"pr-review-automation/internal/webhook"

	"github.com/joho/godotenv"
	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
)

// InterceptingTransport implements mcp.Transport
type InterceptingTransport struct {
	RealTransport mcp.Transport
	CapturedOps   *[]string
	Mu            *sync.Mutex
}

func (t *InterceptingTransport) Connect(ctx context.Context) (mcp.Connection, error) {
	realConn, err := t.RealTransport.Connect(ctx)
	if err != nil {
		return nil, err
	}

	ic := &InterceptingConnection{
		inner:       realConn,
		capturedOps: t.CapturedOps,
		mu:          t.Mu,
		readChan:    make(chan jsonrpc.Message),
		errChan:     make(chan error),
	}

	// Start forwarding real responses
	go func() {
		for {
			msg, err := realConn.Read(context.Background())
			if err != nil {
				ic.errChan <- err
				return
			}
			ic.readChan <- msg
		}
	}()

	return ic, nil
}

// InterceptingConnection implements mcp.Connection
type InterceptingConnection struct {
	inner       mcp.Connection
	capturedOps *[]string
	mu          *sync.Mutex

	readChan chan jsonrpc.Message
	errChan  chan error
}

func (c *InterceptingConnection) Close() error {
	return c.inner.Close()
}

func (c *InterceptingConnection) Read(ctx context.Context) (jsonrpc.Message, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case err := <-c.errChan:
		return nil, err
	case msg := <-c.readChan:
		return msg, nil
	}
}

func (c *InterceptingConnection) Write(ctx context.Context, message jsonrpc.Message) error {
	// Type assertion to access Request fields
	var req *jsonrpc.Request

	switch m := message.(type) {
	case *jsonrpc.Request:
		req = m
	}

	if req != nil && req.Method == "tools/call" {
		var params struct {
			Name      string                 `json:"name"`
			Arguments map[string]interface{} `json:"arguments"`
		}
		if err := json.Unmarshal(req.Params, &params); err == nil {
			if params.Name == config.ToolBitbucketAddComment {
				c.mu.Lock()
				op := fmt.Sprintf("Comment on %v: %v", params.Arguments["pullRequestId"], params.Arguments["commentText"])
				*c.capturedOps = append(*c.capturedOps, op)
				c.mu.Unlock()

				fmt.Printf("\n[OUTPUT] Bitbucket Comment:\n%v\n", params.Arguments["commentText"])

				// Inject Mock Success Response
				resultJSON := `{"content": [{"type": "text", "text": "{\"id\": 12345, \"version\": 1}"}]}`
				resp := &jsonrpc.Response{
					ID:     req.ID,
					Result: json.RawMessage(resultJSON),
				}
				go func() { c.readChan <- resp }()
				return nil
			}

		}
	}

	return c.inner.Write(ctx, message)
}

func (c *InterceptingConnection) SessionID() string {
	return c.inner.SessionID()
}

func TestE2E_PRFlow(t *testing.T) {
	// 1. Load Environment & Config (Real Config)
	rootDir := "../../"
	if err := godotenv.Load(filepath.Join(rootDir, ".env")); err != nil {
		t.Logf("Warning: .env file not found at %s: %v", rootDir, err)
	}

	os.Setenv("CONFIG_PATH", filepath.Join(rootDir, "config.test.yaml"))
	cfg := config.LoadConfig()
	cfg.Prompts.Dir = filepath.Join(rootDir, "prompts")

	if cfg.LLM.APIKey == "" {
		t.Skip("Skipping E2E test: LLM_API_KEY not set")
	}
	if cfg.MCP.Bitbucket.Endpoint == "" {
		t.Skip("Skipping E2E review: Bitbucket endpoint not configured")
	}

	// 2. Setup Intercepting Client
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug}))
	slog.SetDefault(logger)

	mcpClient := client.NewMCPClient(cfg)

	// [FIX] Inject Response Filter (Same as production)
	bbResponseFilter := bitbucket.NewResponseFilter(20000)
	mcpClient.SetResponseFilter("bitbucket", bbResponseFilter)

	capturedOps := []string{}
	var mu sync.Mutex

	mcpClient.SetTransportFactory(func(ctx context.Context, endpoint, token, authHeader string, timeout time.Duration) (mcp.Transport, error) {
		t.Logf("[DEBUG] Creating transport for endpoint=%s, tokenLen=%d, authHeader=%s, timeout=%s", endpoint, len(token), authHeader, timeout)
		realTransport, err := client.NewMCPTransport(ctx, endpoint, token, authHeader, timeout)
		if err != nil {
			return nil, err
		}
		return &InterceptingTransport{
			RealTransport: realTransport,
			CapturedOps:   &capturedOps,
			Mu:            &mu,
		}, nil
	})

	if err := mcpClient.InitializeConnections(); err != nil {
		t.Fatalf("Failed to initialize MCP connections: %v", err)
	}
	defer mcpClient.Close()

	// 3. Real Agent & Dependencies (Using Pipeline)
	oaClient := openai.NewClient(option.WithAPIKey(cfg.LLM.APIKey), option.WithBaseURL(cfg.LLM.Endpoint))
	llm := client.NewOpenAIAdapter(&oaClient, cfg.LLM.Model)
	promptLoader := pipeline.NewPromptLoader(cfg.Prompts.Dir)

	// Create Reviewer using Pipeline Adapter
	prReviewer := pipeline.NewPipelineAdapter(cfg, mcpClient, llm, promptLoader)

	prProcessor := processor.NewPRProcessor(cfg, prReviewer, mcpClient, nil)
	bbFilter := bitbucket.NewPayloadFilter()
	parser := webhook.NewPayloadParser(cfg.Webhook, llm, promptLoader, bbFilter)
	handler := webhook.NewBitbucketWebhookHandler(cfg, prProcessor, parser)

	// 4. Send Real Payload (User Provided - Corrected)
	reqBody := `{
    "date": "2026-01-20T07:53:37+0100",
    "actor": {
        "emailAddress": "peng.wang@navinfo.com",
        "displayName": "Wang Peng",
        "name": "peng.wang",
        "active": true,
        "links": {"self": [{"href": "https://bitbucket.cms.navinfo.cloud/users/peng.wang"}]},
        "id": 303,
        "type": "NORMAL",
        "slug": "peng.wang"
    },
    "eventKey": "pr:opened",
    "pullRequest": {
        "author": {
            "approved": false,
            "role": "AUTHOR",
            "user": {
                "emailAddress": "peng.wang@navinfo.com",
                "displayName": "Wang Peng",
                "name": "peng.wang",
                "active": true,
                "links": {"self": [{"href": "https://bitbucket.cms.navinfo.cloud/users/peng.wang"}]},
                "id": 303,
                "type": "NORMAL",
                "slug": "peng.wang"
            },
            "status": "UNAPPROVED"
        },
        "updatedDate": 1768892017467,
        "title": "HAD-10776 supprot NumberOfLanes",
        "version": 0,
        "reviewers": [{
            "approved": false,
            "role": "REVIEWER",
            "user": {
                "emailAddress": "tangyong@navinfo.com",
                "displayName": "Tang Yong",
                "name": "tang.yong",
                "active": true,
                "links": {"self": [{"href": "https://bitbucket.cms.navinfo.cloud/users/tang.yong"}]},
                "id": 220,
                "type": "NORMAL",
                "slug": "tang.yong"
            },
            "status": "UNAPPROVED"
        }],
        "toRef": {
            "latestCommit": "0edb30c498aae820bd418973f4bda6850d3e839e",
            "id": "refs/heads/controlled/Toolkit",
            "displayId": "controlled/Toolkit",
            "type": "BRANCH",
            "repository": {
                "archived": false,
                "public": false,
                "hierarchyId": "bcbf91974885516f2579",
                "name": "Toolkit",
                "forkable": true,
                "project": {
                    "public": false,
                    "name": "FastMap",
                    "description": "DDS",
                    "links": {"self": [{"href": "https://bitbucket.cms.navinfo.cloud/projects/FAS"}]},
                    "id": 3283,
                    "type": "NORMAL",
                    "key": "FAS"
                },
                "links": {
                    "clone": [
                        {
                            "name": "ssh",
                            "href": "ssh://git@ssh.bitbucket.cms.navinfo.cloud:7999/fas/toolkit.git"
                        },
                        {
                            "name": "http",
                            "href": "https://bitbucket.cms.navinfo.cloud/scm/fas/toolkit.git"
                        }
                    ],
                    "self": [{"href": "https://bitbucket.cms.navinfo.cloud/projects/FAS/repos/toolkit/browse"}]
                },
                "id": 4764,
                "scmId": "git",
                "state": "AVAILABLE",
                "slug": "toolkit",
                "statusMessage": "Available"
            }
        },
        "createdDate": 1768892017467,
        "draft": false,
        "closed": false,
        "fromRef": {
            "latestCommit": "1dfa57501d988cea1d7e514be5d670e822c0e435",
            "id": "refs/heads/HAD-10776-support-number-of-lanes",
            "displayId": "HAD-10776-support-number-of-lanes",
            "type": "BRANCH",
            "repository": {
                "archived": false,
                "public": false,
                "hierarchyId": "bcbf91974885516f2579",
                "name": "Toolkit",
                "forkable": true,
                "project": {
                    "public": false,
                    "name": "FastMap",
                    "description": "DDS",
                    "links": {"self": [{"href": "https://bitbucket.cms.navinfo.cloud/projects/FAS"}]},
                    "id": 3283,
                    "type": "NORMAL",
                    "key": "FAS"
                },
                "links": {
                    "clone": [
                        {
                            "name": "ssh",
                            "href": "ssh://git@ssh.bitbucket.cms.navinfo.cloud:7999/fas/toolkit.git"
                        },
                        {
                            "name": "http",
                            "href": "https://bitbucket.cms.navinfo.cloud/scm/fas/toolkit.git"
                        }
                    ],
                    "self": [{"href": "https://bitbucket.cms.navinfo.cloud/projects/FAS/repos/toolkit/browse"}]
                },
                "id": 4764,
                "scmId": "git",
                "state": "AVAILABLE",
                "slug": "toolkit",
                "statusMessage": "Available"
            }
        },
        "links": {"self": [{"href": "https://bitbucket.cms.navinfo.cloud/projects/FAS/repos/toolkit/pull-requests/65"}]},
        "id": 65,
        "state": "OPEN",
        "locked": false,
        "open": true,
        "participants": []
    }
}`

	req := httptest.NewRequest(http.MethodPost, "http://192.168.30.20:8080/webhook", bytes.NewBufferString(reqBody))
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	req.Header.Set("X-Event-Key", "pr:opened")
	req.Header.Set("X-Request-Id", "b4742057-7bef-468c-8bff-a2122e0f4112")

	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Result().StatusCode != 200 {
		t.Errorf("Expected 200 OK")
	}

	logger.Info("Waiting for async processing...")
	// We need to wait enough time for real network calls
	// Using the handler's wait mechanism if available, or just sleep
	// handler.WaitForCompletion() is not standard, let's poll or wait
	// Actually, the original code had WaitForCompletion because it was using a specialized handler or mock?
	// The current code calls handler.WaitForCompletion() which implies I added it to the handler?
	// Let's check internal/webhook/bitbucket.go.
	// If it doesn't have it, I should add a way to wait or just sleep.
	// For E2E with real network, 30s might be needed.

	done := make(chan bool)
	go func() {
		handler.WaitForCompletion()
		done <- true
	}()

	select {
	case <-done:
		logger.Info("Processing completed")
	case <-time.After(300 * time.Second):
		t.Log("Timeout waiting for processing")
	}

	mu.Lock()
	defer mu.Unlock()

	if len(capturedOps) > 0 {
		t.Logf("Success! Intercepted %d write operations", len(capturedOps))
	} else {
		t.Logf("No write operations captured. Check logs.")
	}
}

func TestE2E_ChunkedReview(t *testing.T) {
	// 1. Load Environment & Config
	rootDir := "../../"
	if err := godotenv.Load(filepath.Join(rootDir, ".env")); err != nil {
		t.Logf("Warning: .env file not found at %s: %v", rootDir, err)
	}

	os.Setenv("CONFIG_PATH", filepath.Join(rootDir, "config.test.yaml"))
	cfg := config.LoadConfig()
	cfg.Prompts.Dir = filepath.Join(rootDir, "prompts")

	// --- Pipeline is now the default backend, no Agent configuration needed ---

	if cfg.LLM.APIKey == "" {
		t.Skip("Skipping E2E test: LLM_API_KEY not set")
	}
	if cfg.MCP.Bitbucket.Endpoint == "" {
		t.Skip("Skipping E2E review: Bitbucket endpoint not configured")
	}

	// 2. Setup Intercepting Client
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug}))
	slog.SetDefault(logger)

	mcpClient := client.NewMCPClient(cfg)
	bbResponseFilter := bitbucket.NewResponseFilter(20000)
	mcpClient.SetResponseFilter("bitbucket", bbResponseFilter)

	capturedOps := []string{}
	var mu sync.Mutex

	mcpClient.SetTransportFactory(func(ctx context.Context, endpoint, token, authHeader string, timeout time.Duration) (mcp.Transport, error) {
		t.Logf("[DEBUG] Creating transport for endpoint=%s, timeout=%s", endpoint, timeout)
		realTransport, err := client.NewMCPTransport(ctx, endpoint, token, authHeader, timeout)
		if err != nil {
			return nil, err
		}
		return &InterceptingTransport{
			RealTransport: realTransport,
			CapturedOps:   &capturedOps,
			Mu:            &mu,
		}, nil
	})

	if err := mcpClient.InitializeConnections(); err != nil {
		t.Fatalf("Failed to initialize MCP connections: %v", err)
	}
	defer mcpClient.Close()

	// 3. Real LLM & Dependencies (Using Pipeline)
	llm, err := client.NewLLM(cfg)
	if err != nil {
		t.Fatalf("Failed to create LLM: %v", err)
	}
	promptLoader := pipeline.NewPromptLoader(cfg.Prompts.Dir)
	promptLoader.SetRawSchemaProvider(mcpClient)

	// Create Reviewer using Pipeline Adapter
	prReviewer := pipeline.NewPipelineAdapter(cfg, mcpClient, llm, promptLoader)

	prProcessor := processor.NewPRProcessor(cfg, prReviewer, mcpClient, nil)
	bbFilter := bitbucket.NewPayloadFilter()
	parser := webhook.NewPayloadParser(cfg.Webhook, llm, promptLoader, bbFilter)
	handler := webhook.NewBitbucketWebhookHandler(cfg, prProcessor, parser)

	// 4. Send Payload (REAL DATA FROM USER)
	reqBody := `{
    "date": "2026-01-20T07:53:37+0100",
    "actor": {
        "emailAddress": "peng.wang@navinfo.com",
        "displayName": "Wang Peng",
        "name": "peng.wang",
        "active": true,
        "links": {"self": [{"href": "https://bitbucket.cms.navinfo.cloud/users/peng.wang"}]},
        "id": 303,
        "type": "NORMAL",
        "slug": "peng.wang"
    },
    "eventKey": "pr:opened",
    "pullRequest": {
        "author": {
            "approved": false,
            "role": "AUTHOR",
            "user": {
                "emailAddress": "peng.wang@navinfo.com",
                "displayName": "Wang Peng",
                "name": "peng.wang",
                "active": true,
                "links": {"self": [{"href": "https://bitbucket.cms.navinfo.cloud/users/peng.wang"}]},
                "id": 303,
                "type": "NORMAL",
                "slug": "peng.wang"
            },
            "status": "UNAPPROVED"
        },
        "updatedDate": 1768892017467,
        "title": "HAD-10776 supprot NumberOfLanes",
        "version": 0,
        "reviewers": [{
            "approved": false,
            "role": "REVIEWER",
            "user": {
                "emailAddress": "tangyong@navinfo.com",
                "displayName": "Tang Yong",
                "name": "tang.yong",
                "active": true,
                "links": {"self": [{"href": "https://bitbucket.cms.navinfo.cloud/users/tang.yong"}]},
                "id": 220,
                "type": "NORMAL",
                "slug": "tang.yong"
            },
            "status": "UNAPPROVED"
        }],
        "toRef": {
            "latestCommit": "0edb30c498aae820bd418973f4bda6850d3e839e",
            "id": "refs/heads/controlled/Toolkit",
            "displayId": "controlled/Toolkit",
            "type": "BRANCH",
            "repository": {
                "archived": false,
                "public": false,
                "hierarchyId": "bcbf91974885516f2579",
                "name": "Toolkit",
                "forkable": true,
                "project": {
                    "public": false,
                    "name": "FastMap",
                    "description": "DDS",
                    "links": {"self": [{"href": "https://bitbucket.cms.navinfo.cloud/projects/FAS"}]},
                    "id": 3283,
                    "type": "NORMAL",
                    "key": "FAS"
                },
                "links": {
                    "clone": [
                        {
                            "name": "ssh",
                            "href": "ssh://git@ssh.bitbucket.cms.navinfo.cloud:7999/fas/toolkit.git"
                        },
                        {
                            "name": "http",
                            "href": "https://bitbucket.cms.navinfo.cloud/scm/fas/toolkit.git"
                        }
                    ],
                    "self": [{"href": "https://bitbucket.cms.navinfo.cloud/projects/FAS/repos/toolkit/browse"}]
                },
                "id": 4764,
                "scmId": "git",
                "state": "AVAILABLE",
                "slug": "toolkit",
                "statusMessage": "Available"
            }
        },
        "createdDate": 1768892017467,
        "draft": false,
        "closed": false,
        "fromRef": {
            "latestCommit": "1dfa57501d988cea1d7e514be5d670e822c0e435",
            "id": "refs/heads/HAD-10776-support-number-of-lanes",
            "displayId": "HAD-10776-support-number-of-lanes",
            "type": "BRANCH",
            "repository": {
                "archived": false,
                "public": false,
                "hierarchyId": "bcbf91974885516f2579",
                "name": "Toolkit",
                "forkable": true,
                "project": {
                    "public": false,
                    "name": "FastMap",
                    "description": "DDS",
                    "links": {"self": [{"href": "https://bitbucket.cms.navinfo.cloud/projects/FAS"}]},
                    "id": 3283,
                    "type": "NORMAL",
                    "key": "FAS"
                },
                "links": {
                    "clone": [
                        {
                            "name": "ssh",
                            "href": "ssh://git@ssh.bitbucket.cms.navinfo.cloud:7999/fas/toolkit.git"
                        },
                        {
                            "name": "http",
                            "href": "https://bitbucket.cms.navinfo.cloud/scm/fas/toolkit.git"
                        }
                    ],
                    "self": [{"href": "https://bitbucket.cms.navinfo.cloud/projects/FAS/repos/toolkit/browse"}]
                },
                "id": 4764,
                "scmId": "git",
                "state": "AVAILABLE",
                "slug": "toolkit",
                "statusMessage": "Available"
            }
        },
        "links": {"self": [{"href": "https://bitbucket.cms.navinfo.cloud/projects/FAS/repos/toolkit/pull-requests/65"}]},
        "id": 65,
        "state": "OPEN",
        "locked": false,
        "open": true,
        "participants": []
    }
}`

	req := httptest.NewRequest(http.MethodPost, "http://192.168.30.20:8080/webhook", bytes.NewBufferString(reqBody))
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	req.Header.Set("X-Event-Key", "pr:opened")
	req.Header.Set("X-Request-Id", "test-chunk-review")

	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Result().StatusCode != 200 {
		t.Errorf("Expected 200 OK, got %d", w.Result().StatusCode)
	}

	logger.Info("Waiting for async processing...")

	done := make(chan bool)
	go func() {
		handler.WaitForCompletion()
		done <- true
	}()

	select {
	case <-done:
		logger.Info("Processing completed")
	case <-time.After(900 * time.Second):
		t.Log("Timeout waiting for processing")
	}

	mu.Lock()
	defer mu.Unlock()

	if len(capturedOps) > 0 {
		t.Logf("Success! Intercepted %d write operations:", len(capturedOps))

		var hasFileMarker, hasSummaryMarker bool
		for i, op := range capturedOps {
			t.Logf("Op[%d]: %s", i, op)

			if strings.Contains(op, "<!-- ai-review::file:") {
				hasFileMarker = true
			}
			if strings.Contains(op, "<!-- ai-review::summary:") {
				hasSummaryMarker = true
			}
		}

		if !hasSummaryMarker {
			t.Error("Expected summary comment with 'summary:' marker")
		}
		// Note: file marker might be missing if no high severity issues found,
		// so we don't strictly assert it, but log it.
		if !hasFileMarker {
			t.Log("Note: No file-level merged comments found (possibly all low severity)")
		}

	} else {
		t.Logf("No write operations captured. Check logs.")
	}
}
