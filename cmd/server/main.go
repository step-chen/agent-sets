package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"pr-review-automation/internal/agent"
	"pr-review-automation/internal/client"
	"pr-review-automation/internal/config"
	"pr-review-automation/internal/processor"
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
	logger := setupLogger(cfg)
	slog.SetDefault(logger)

	// Initialize clients
	mcpClient := client.NewMCPClient(cfg)

	// Create LLM once at startup
	llm, err := client.NewLLM(context.Background(), cfg)
	if err != nil {
		slog.Error("create llm failed", "error", err)
		os.Exit(1)
	}

	// Create a context for initialization
	if err := mcpClient.InitializeConnections(); err != nil {
		slog.Error("init mcp failed", "error", err)
		// Proceeding, as some might have failed but others succeeded, or we might want to run without tools
	}
	defer mcpClient.Close()

	// Initialize PR review agent
	prReviewAgent, err := agent.NewPRReviewAgent(llm, mcpClient, cfg.Agent.PRReviewPromptName)
	if err != nil {
		slog.Error("init agent failed", "error", err)
		os.Exit(1)
	}

	// Initialize PR processor
	prProcessor := processor.NewPRProcessor(prReviewAgent, mcpClient)

	// Initialize webhook handler
	webhookHandler := webhook.NewBitbucketWebhookHandler(cfg, prProcessor)

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

	// Legacy /health aliases to /health/ready for backward compatibility
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		if !mcpClient.IsHealthy() {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
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

	// Wait for background PR processing tasks
	slog.Info("waiting for tasks")
	done := make(chan struct{})
	go func() {
		webhookHandler.WaitForCompletion()
		close(done)
	}()

	select {
	case <-done:
		slog.Info("tasks completed")
	case <-time.After(30 * time.Second):
		slog.Warn("task timeout, exiting")
	}

	slog.Info("server stopped")
}

// setupLogger creates a logger based on configuration
func setupLogger(cfg *config.Config) *slog.Logger {
	var w io.Writer
	switch cfg.Log.Output {
	case "stderr":
		w = os.Stderr
	case "stdout", "":
		w = os.Stdout
	default:
		f, err := os.OpenFile(cfg.Log.Output, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
		if err != nil {
			fmt.Fprintf(os.Stderr, "open log file failed: %v, using stdout\n", err)
			w = os.Stdout
		} else {
			w = f
		}
	}

	opts := &slog.HandlerOptions{Level: cfg.GetLogLevel()}

	var handler slog.Handler
	if cfg.Log.Format == "json" {
		handler = slog.NewJSONHandler(w, opts)
	} else {
		handler = slog.NewTextHandler(w, opts)
	}

	return slog.New(handler)
}
