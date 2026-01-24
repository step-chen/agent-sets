package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"gopkg.in/natefinch/lumberjack.v2"

	"pr-review-automation/internal/client"
	"pr-review-automation/internal/config"
	"pr-review-automation/internal/filter/bitbucket"
	"pr-review-automation/internal/pipeline"
	"pr-review-automation/internal/processor"
	"pr-review-automation/internal/storage"
	"pr-review-automation/internal/webhook"

	"github.com/prometheus/client_golang/prometheus/promhttp"
)

func main() {

	// Load configuration first
	cfg := config.LoadConfig()

	// Validate configuration
	if err := cfg.Validate(); err != nil {
		fmt.Fprintf(os.Stderr, "Configuration error: %v\n", err)
		os.Exit(1)
	}

	// Setup structured logging with configurable level, format, and output
	logger, logCleanup := setupLogger(cfg)
	defer logCleanup()
	slog.SetDefault(logger)

	// Initialize clients
	mcpClient := client.NewMCPClient(cfg)

	// Create LLM once at startup
	llm, err := client.NewLLM(cfg)
	if err != nil {
		slog.Error("create llm failed", "error", err)
		os.Exit(1)
	}

	// Verify LLM connection
	if checker, ok := llm.(interface{ Ping(context.Context) error }); ok {
		if err := checker.Ping(context.Background()); err != nil {
			slog.Error("llm health check failed", "error", err)
			os.Exit(1)
		}
	}

	// Initialize Filters
	bbPayloadFilter := bitbucket.NewPayloadFilter()
	bbResponseFilter := bitbucket.NewResponseFilter(cfg.Pipeline.ResponseMaxStringLen)

	// Register filters with MCP Client
	mcpClient.SetResponseFilter("bitbucket", bbResponseFilter)
	mcpClient.SetResponseFilter("jira", bbResponseFilter)
	mcpClient.SetResponseFilter("confluence", bbResponseFilter)

	// Create a context for initialization
	if err := mcpClient.InitializeConnections(); err != nil {
		slog.Error("init mcp failed", "error", err)
		// Proceeding, as some might have failed but others succeeded, or we might want to run without tools
	}
	defer mcpClient.Close()

	// Initialize Prompt Loader (Pipeline version)
	promptLoader := pipeline.NewPromptLoader(cfg.Prompts.Dir)
	promptLoader.SetRawSchemaProvider(mcpClient)

	// Initialize PR review agent using Pipeline Adapter
	prReviewer := pipeline.NewPipelineAdapter(cfg, mcpClient, llm, promptLoader)
	slog.Info("reviewer initialized", "backend", prReviewer.Name())

	// Initialize storage
	var store storage.Repository
	if cfg.Storage.Driver == "sqlite" {
		var err error
		store, err = storage.NewSQLiteRepository(cfg.Storage.DSN)
		if err != nil {
			slog.Error("init storage failed", "error", err)
			os.Exit(1)
		}
		defer store.Close()
	} else if cfg.Storage.Driver != "" {
		slog.Warn("unknown storage driver", "driver", cfg.Storage.Driver)
	}

	// Initialize PR processor
	// Note: PRProcessor now uses domain types and generic Reviewer interface
	prProcessor := processor.NewPRProcessor(cfg, prReviewer, mcpClient, store)

	// Initialize Payload Parser with filter
	// Need to ensure payloadParser uses generic promptLoader or pipeline one
	// payloadParser usually uses agent prompt loader. We might need to adapter or use pipeline.PromptLoader if compatible.
	// Since we defined pipeline.PromptLoader similarly, we should check what NewPayloadParser expects.
	// Assume we refactor PayloadParser to accept domain.PromptLoader or similar.
	// For now, let's keep it if compatible or cast it.
	// But agent.PromptLoader is different type than pipeline.PromptLoader.
	// We need to fix PayloadParser too.
	// Let's assume for now PayloadParser uses the interface or similar methods.
	// If compilation fails, we will fix PayloadParser.
	// Wait, we should probably update PayloadParser to use pipeline.PromptLoader or domain.PromptLoader interface if generic.
	// But for this change, let's try to pass it if compatible (structs are not compatible unless same type).
	// So we need to update NewPayloadParser signature in webhook package.
	// Or define PromptLoader in domain.

	// Temporarily: use pipeline.PromptLoader and changing PayloadParser signature is best.
	payloadParser := webhook.NewPayloadParser(cfg.Webhook, llm, promptLoader, bbPayloadFilter)

	// Initialize webhook handler
	webhookHandler := webhook.NewBitbucketWebhookHandler(cfg, prProcessor, payloadParser)

	// Setup HTTP server
	mux := http.NewServeMux()
	mux.Handle("/webhook", webhookHandler)

	// Liveness probe (Kubernetes: startup/liveness)
	mux.HandleFunc("/health/live", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	})

	// Readiness probe (Kubernetes: readiness)
	// Checks if all dependencies are healthy
	mux.HandleFunc("/health/ready", func(w http.ResponseWriter, r *http.Request) {
		if !mcpClient.IsHealthy() {
			slog.Warn("mcp unhealthy")
			http.Error(w, "MCP Service Unavailable", http.StatusServiceUnavailable)
			return
		}
		// Could also check LLM connectivity here if needed
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("Ready"))
	})

	// Add root path handler to catch misconfiguration (e.g. omitted /webhook in URL)
	// It logs a helpful warning but still returns 404 to be semantically correct.
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			slog.Warn("received request at root path",
				"path", r.URL.Path,
				"method", r.Method,
				"msg", "please configure webhook URL to path '/webhook'",
			)
		}
		http.NotFound(w, r)
	})

	// Prometheus Metrics Endpoint
	mux.Handle("/metrics", promhttp.Handler())

	server := &http.Server{
		Addr:         fmt.Sprintf(":%d", cfg.Server.Port),
		Handler:      mux,
		ReadTimeout:  cfg.Server.ReadTimeout,
		WriteTimeout: cfg.Server.WriteTimeout,
	}

	// Start server in a goroutine
	go func() {
		slog.Info("server starting", "port", cfg.Server.Port)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("server start failed", "error", err)
			os.Exit(1)
		}
	}()

	// Wait for interrupt signal to gracefully shutdown the server
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	slog.Info("server stopping")

	// Give the server 5 seconds to shutdown gracefully
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := server.Shutdown(ctx); err != nil {
		slog.Error("server shutdown forced", "error", err)
		os.Exit(1)
	}

	// Wait for background tasks to complete with timeout
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), cfg.Server.ShutdownTimeout)
	defer shutdownCancel()

	done := make(chan struct{})
	go func() {
		webhookHandler.WaitForCompletion()
		close(done)
	}()

	select {
	case <-done:
		slog.Info("background tasks completed")
	case <-shutdownCtx.Done():
		slog.Warn("task timeout, exiting", "timeout", cfg.Server.ShutdownTimeout)
	}

	// 3. defer store.Close() will handle storage cleanup (via WAL checkpoint)

	slog.Info("server stopped")
}

// setupLogger creates a logger based on configuration
func setupLogger(cfg *config.Config) (*slog.Logger, func()) {
	var writers []io.Writer
	var closers []io.Closer
	outputs := strings.Split(cfg.Log.Output, ",")

	for _, output := range outputs {
		output = strings.TrimSpace(output)
		if output == "" {
			continue
		}

		var w io.Writer
		switch output {
		case "stderr":
			w = os.Stderr
		case "stdout":
			w = os.Stdout
		default:
			// Use lumberjack for log rotation
			l := &lumberjack.Logger{
				Filename:   output,
				MaxSize:    cfg.Log.Rotation.MaxSize,
				MaxBackups: cfg.Log.Rotation.MaxBackups,
				MaxAge:     cfg.Log.Rotation.MaxAge,
				Compress:   cfg.Log.Rotation.Compress,
			}
			w = l
			closers = append(closers, l)
		}
		writers = append(writers, w)
	}

	if len(writers) == 0 {
		writers = append(writers, os.Stdout)
	}

	multiWriter := io.MultiWriter(writers...)
	opts := &slog.HandlerOptions{Level: cfg.GetLogLevel()}

	var handler slog.Handler
	if cfg.Log.Format == "json" {
		handler = slog.NewJSONHandler(multiWriter, opts)
	} else {
		handler = slog.NewTextHandler(multiWriter, opts)
	}

	cleanup := func() {
		for _, c := range closers {
			c.Close()
		}
	}

	return slog.New(handler), cleanup
}
