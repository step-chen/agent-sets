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
	"sort"
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
)

// InterceptingTransport implements mcp.Transport
type InterceptingTransport struct {
	RealTransport mcp.Transport
	OnMessage     func(jsonrpc.Message) jsonrpc.Message // Callback to intercept/modify messages
}

func (t *InterceptingTransport) Connect(ctx context.Context) (mcp.Connection, error) {
	realConn, err := t.RealTransport.Connect(ctx)
	if err != nil {
		return nil, err
	}

	ic := &InterceptingConnection{
		inner:     realConn,
		onMessage: t.OnMessage,
		readChan:  make(chan jsonrpc.Message),
		errChan:   make(chan error),
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
	inner     mcp.Connection
	onMessage func(jsonrpc.Message) jsonrpc.Message // Return nil to block, or new msg to return

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
	if c.onMessage != nil {
		response := c.onMessage(message)
		if response != nil {
			// If onMessage returns a response, inject it back to the reader asynchronously
			// and DO NOT forward the original message to the real transport.
			go func() { c.readChan <- response }()
			return nil
		}
	}
	return c.inner.Write(ctx, message)
}

func (c *InterceptingConnection) SessionID() string {
	return c.inner.SessionID()
}

func TestE2E_Main(t *testing.T) {
	// 1. Load Environment & Config
	rootDir := "../../" // Relative path to project root from test/e2e

	// Try loading .env from current directory (expected: test/e2e/.env)
	if err := godotenv.Load(); err != nil {
		// Fallback to root .env if local is missing
		if err := godotenv.Load(filepath.Join(rootDir, ".env")); err != nil {
			t.Logf("Warning: .env not found in current dir or root: %v", err)
		}
	}

	// Force load local E2E config
	os.Setenv("CONFIG_PATH", "config.test.yaml")
	cfg := config.LoadConfig()
	cfg.Prompts.Dir = filepath.Join(rootDir, "prompts")

	if cfg.LLM.APIKey == "" {
		t.Skip("Skipping E2E test: LLM_API_KEY not set")
	}
	if cfg.MCP.Bitbucket.Endpoint == "" {
		t.Skip("Skipping E2E review: Bitbucket endpoint not configured")
	}

	// 2. Setup Logger
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug}))
	slog.SetDefault(logger)

	// 3. Shared State for Interception
	var (
		mu           sync.Mutex
		currentFile  string
		capturedData = make(map[string][]string) // FileName -> []Comments
	)

	// 4. Setup MCP Client with Interceptor
	mcpClient := client.NewMCPClient(cfg)
	// Inject Response Filter
	bbResponseFilter := bitbucket.NewResponseFilter(20000)
	mcpClient.SetResponseFilter("bitbucket", bbResponseFilter)

	mcpClient.SetTransportFactory(func(ctx context.Context, endpoint, token, authHeader string, timeout time.Duration) (mcp.Transport, error) {
		realTransport, err := client.NewMCPTransport(ctx, endpoint, token, authHeader, timeout)
		if err != nil {
			return nil, err
		}
		return &InterceptingTransport{
			RealTransport: realTransport,
			OnMessage: func(msg jsonrpc.Message) jsonrpc.Message {
				req, ok := msg.(*jsonrpc.Request)
				if !ok || req.Method != "tools/call" {
					return nil
				}

				var params struct {
					Name      string                 `json:"name"`
					Arguments map[string]interface{} `json:"arguments"`
				}
				if err := json.Unmarshal(req.Params, &params); err != nil {
					return nil
				}

				if params.Name == config.ToolBitbucketAddComment {
					mu.Lock()
					file := currentFile
					comment := fmt.Sprintf("Path: %v | LineType: %v | Comment: %v", params.Arguments["filePath"], params.Arguments["lineType"], params.Arguments["commentText"])
					capturedData[file] = append(capturedData[file], comment)
					mu.Unlock()

					// Mock Success Response
					resultJSON := `{"content": [{"type": "text", "text": "{\"id\": 12345, \"version\": 1}"}]}`
					return &jsonrpc.Response{
						ID:     req.ID,
						Result: json.RawMessage(resultJSON),
					}
				}
				return nil
			},
		}, nil
	})

	if err := mcpClient.InitializeConnections(); err != nil {
		t.Fatalf("Failed to initialize MCP connections: %v", err)
	}
	defer mcpClient.Close()

	// 5. Setup Pipeline Components (Shared)
	llm, err := client.NewLLM(cfg)
	if err != nil {
		t.Fatalf("Failed to create LLM: %v", err)
	}
	promptLoader := pipeline.NewPromptLoader(cfg.Prompts.Dir)
	promptLoader.SetRawSchemaProvider(mcpClient)

	// Create Reviewer & Processor
	prReviewer := pipeline.NewPipelineAdapter(cfg, mcpClient, llm, promptLoader)
	prProcessor := processor.NewPRProcessor(cfg, prReviewer, mcpClient, nil)

	// Initialize Handler ONCE (Simulating a long-running server)
	bbFilter := bitbucket.NewPayloadFilter()
	parser := webhook.NewPayloadParser(cfg.Webhook, llm, promptLoader, bbFilter)
	handler := webhook.NewBitbucketWebhookHandler(cfg, prProcessor, parser)

	// 6. Test Loop
	reqDir := "requests"
	files, err := filepath.Glob(filepath.Join(reqDir, "*.json"))
	if err != nil {
		t.Fatalf("Failed to list request files: %v", err)
	}
	sort.Strings(files)
	t.Logf("Found %d request files: %v", len(files), files)

	for _, file := range files {
		fileName := filepath.Base(file)

		mu.Lock()
		currentFile = fileName
		mu.Unlock()

		t.Logf("Processing request: %s", fileName)

		// Read Body
		body, err := os.ReadFile(file)
		if err != nil {
			t.Fatalf("Failed to read file %s: %v", file, err)
		}

		// Send Request
		req := httptest.NewRequest(http.MethodPost, "http://localhost:8080/webhook", bytes.NewBuffer(body))
		req.Header.Set("Content-Type", "application/json; charset=utf-8")
		// Extract Event Key from JSON (Simple parse)
		var partial struct {
			EventKey string `json:"eventKey"`
		}
		_ = json.Unmarshal(body, &partial)
		req.Header.Set("X-Event-Key", partial.EventKey)
		req.Header.Set("X-Request-Id", fmt.Sprintf("e2e-%s", fileName))

		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)

		if w.Result().StatusCode != 200 {
			t.Errorf("Request %s failed with status %d", fileName, w.Result().StatusCode)
		}

		// Wait for Debounce Window to pass before sending the next request.
		// This simulates distinct events occurring over time (e.g. Open... wait... Comment).
		// If we didn't wait, the Debouncer would merge these requests into a single execution.
		// We DO NOT wait for completion here; we let the WorkerPool manage the queue/concurrency.
		waitDuration := cfg.Server.DebounceWindow + 2*time.Second
		t.Logf("Request sent. Sleeping %v to simulate event spacing (allowing debounce to fire)...", waitDuration)
		time.Sleep(waitDuration)
	}

	// 7. Wait for ALL processing to complete
	// This stops the worker pool, which waits for all active and queued jobs to finish.
	t.Log("All requests sent. Waiting for worker pool to drain...")
	handler.WaitForCompletion()
	t.Log("All processing completed.")

	// 8. Print Report
	fmt.Println("\n=== E2E Test Result Summary ===")

	// Create a sorted list of keys for deterministic output
	mu.Lock()
	var keys []string
	for k := range capturedData {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	for _, k := range keys {
		fmt.Printf("\n>>> Processed Request: %s\n", k)
		fmt.Println("-----------------------------------")
		comments := capturedData[k]
		if len(comments) == 0 {
			fmt.Println("[NO COMMENTS CAPTURED]")
		} else {
			for i, c := range comments {
				fmt.Printf("[Comment %d]\n%s\n", i+1, c)
			}
		}
		fmt.Println("==============================")
	}
	mu.Unlock()
}
