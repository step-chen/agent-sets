package client

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"golang.org/x/sync/singleflight"
	"google.golang.org/adk/agent"
	"google.golang.org/adk/model"
	"google.golang.org/adk/tool"
	"google.golang.org/adk/tool/mcptoolset"
	"google.golang.org/genai"

	"pr-review-automation/internal/config"
	"pr-review-automation/internal/filter"
	"pr-review-automation/internal/metrics"
	"pr-review-automation/internal/types"
)

// TransportFactory creates a new transport
type TransportFactory func(ctx context.Context, endpoint, token, authHeader string) (mcp.Transport, error)

// circuitState represents the state of a circuit breaker for a single MCP server
type circuitState struct {
	failures    int       // Consecutive failure count
	lastFailure time.Time // Time of last failure
	openUntil   time.Time // Circuit open until this time (requests are rejected)
}

// isOpen returns true if the circuit is open and should reject requests
func (cs *circuitState) isOpen() bool {
	if cs.openUntil.IsZero() {
		return false
	}
	return time.Now().Before(cs.openUntil)
}

// MCPClient manages connections to MCP servers
type MCPClient struct {
	cfg              *config.Config
	transports       map[string]mcp.Transport
	toolsets         map[string]tool.Toolset          // Native toolsets
	endpoints        map[string]endpointInfo          // Store endpoint info for reconnection
	stale            map[string]bool                  // Track stale connections
	circuits         map[string]*circuitState         // Circuit breaker state per server
	responseFilters  map[string]filter.ResponseFilter // [NEW] Response filters per server
	mu               sync.RWMutex                     // Thread-safe access
	transportFactory TransportFactory                 // Factory for creating transports (injectable for testing)
	requestGroup     singleflight.Group               // Singleflight group for coalescing reconnections
	baseCtx          context.Context                  // Lifecycle context for the client and its transports
	cancel           context.CancelFunc               // Cancel function to cleanup resources on Close
}

// SetTransportFactory allows tests to inject a mock transport factory
func (c *MCPClient) SetTransportFactory(tf TransportFactory) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.transportFactory = tf
}

// SetResponseFilter sets a response filter for a specific server (provider)
func (c *MCPClient) SetResponseFilter(serverName string, f filter.ResponseFilter) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.responseFilters == nil {
		c.responseFilters = make(map[string]filter.ResponseFilter)
	}
	c.responseFilters[serverName] = f
}

// Circuit breaker constants
const (
	circuitFailureThreshold = 3                // Open circuit after N consecutive failures
	circuitOpenDuration     = 30 * time.Second // Keep circuit open for this duration
)

// endpointInfo stores connection configuration for reconnection
type endpointInfo struct {
	endpoint     string
	token        string
	authHeader   string   // Header name for token
	allowedTools []string // Whitelist of tool names to expose
}

// NewMCPClient creates a new MCP client manager
func NewMCPClient(cfg *config.Config) *MCPClient {
	ctx, cancel := context.WithCancel(context.Background())
	return &MCPClient{
		cfg:              cfg,
		transports:       make(map[string]mcp.Transport),
		toolsets:         make(map[string]tool.Toolset),
		endpoints:        make(map[string]endpointInfo),
		stale:            make(map[string]bool),
		circuits:         make(map[string]*circuitState),
		transportFactory: NewMCPTransport, // Default to standard transport factory
		baseCtx:          ctx,
		cancel:           cancel,
	}
}

// InitializeConnections establishes connections to configured MCP servers
func (c *MCPClient) InitializeConnections() error {

	addServerConn := func(name string, serverCfg config.MCPServerConfig) error {
		if serverCfg.Endpoint == "" {
			slog.Debug("mcp server disabled", "server", name)
			return nil
		}
		c.mu.Lock()
		c.endpoints[name] = endpointInfo{
			endpoint:     serverCfg.Endpoint,
			token:        serverCfg.Token,
			authHeader:   serverCfg.AuthHeader,
			allowedTools: serverCfg.AllowedTools,
		}
		c.mu.Unlock()

		_, err := c.getOrReconnect(name)
		if err != nil {
			slog.Error("connect mcp failed", "server", name, "error", err)
			return err
		}
		return nil
	}

	addServerConn("bitbucket", c.cfg.MCP.Bitbucket)
	addServerConn("jira", c.cfg.MCP.Jira)
	addServerConn("confluence", c.cfg.MCP.Confluence)

	return nil
}

// GetToolsets returns all initialized toolsets for agent use
// It creates RetryToolset instances to ensure retries are handled.
func (c *MCPClient) GetToolsets() []tool.Toolset {
	c.mu.RLock()
	defer c.mu.RUnlock()
	result := make([]tool.Toolset, 0, len(c.endpoints))

	for name := range c.endpoints {
		// Use RetryToolset to wrap the client and server name
		result = append(result, &RetryToolset{
			client:     c,
			serverName: name,
		})
	}
	return result
}

// IsHealthy checks if all configured connections are healthy
func (c *MCPClient) IsHealthy() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()

	for name := range c.endpoints {
		if c.stale[name] {
			return false
		}
		// Check if transport exists
		if _, ok := c.transports[name]; !ok {
			return false
		}
	}
	return true
}

// Close releases MCP resources.
// It iterates through all managed transports and proactively closes them.
// This is critical for preventing zombie processes when using stdio transports.
func (c *MCPClient) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Cancel the base context to stop any background processes (like stdio transports)
	if c.cancel != nil {
		slog.Debug("closing mcp client")
		c.cancel()
	}

	var errs []error

	for name, transport := range c.transports {
		// Attempt to close the transport if it implements io.Closer
		if closer, ok := transport.(io.Closer); ok {
			if err := closer.Close(); err != nil {
				slog.Error("close transport failed", "server", name, "error", err)
				errs = append(errs, fmt.Errorf("close %s: %w", name, err))
			} else {
				slog.Debug("transport closed", "server", name)
			}
		} else {
			slog.Debug("transport has no Close", "server", name)
		}

		delete(c.transports, name)
		delete(c.toolsets, name)
	}

	if len(errs) > 0 {
		return fmt.Errorf("close transports: %v", errs)
	}

	return nil
}

// getNativeToolset returns the current native toolset for a server
func (c *MCPClient) getNativeToolset(name string) (tool.Toolset, error) {
	c.mu.RLock()
	ts, ok := c.toolsets[name]
	c.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("toolset not found: %s", name)
	}
	return ts, nil
}

// getOrReconnect returns existing toolset or reconnects if stale
func (c *MCPClient) getOrReconnect(name string) (tool.Toolset, error) {
	c.mu.RLock()
	logger := slog.With("server", name)
	ts, hasTs := c.toolsets[name]
	isStale := c.stale[name]
	circuit := c.circuits[name]
	c.mu.RUnlock()

	logger.Debug("get toolset", "exists", hasTs, "stale", isStale)

	// Circuit breaker check - fast fail if circuit is open
	if circuit != nil && circuit.isOpen() {
		logger.Debug("Circuit breaker open, rejecting request",
			"open_until", circuit.openUntil,
			"failures", circuit.failures)
		metrics.MCPToolCalls.WithLabelValues(name, "circuit_breaker", "rejected").Inc()
		return nil, fmt.Errorf("circuit open: %s, retry after %v", name, time.Until(circuit.openUntil))
	}

	if hasTs && !isStale {
		return ts, nil
	}

	// Use singleflight to deduplicate concurrent reconnection attempts
	val, err, _ := c.requestGroup.Do(name, func() (interface{}, error) {
		// Double check inside the singleflight to see if another call just finished it
		c.mu.RLock()
		ts, hasTs := c.toolsets[name]
		isStale := c.stale[name]
		c.mu.RUnlock()

		if hasTs && !isStale {
			return ts, nil
		}

		return c.reconnect(name, logger)
	})

	if err != nil {
		// Update circuit breaker state on failure
		c.recordFailure(name)
		return nil, err
	}
	return val.(tool.Toolset), nil
}

// recordFailure updates circuit breaker state after a connection failure
func (c *MCPClient) recordFailure(name string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	circuit := c.circuits[name]
	if circuit == nil {
		circuit = &circuitState{}
		c.circuits[name] = circuit
	}

	circuit.failures++
	circuit.lastFailure = time.Now()

	if circuit.failures >= circuitFailureThreshold {
		circuit.openUntil = time.Now().Add(circuitOpenDuration)
		slog.Warn("circuit breaker opened",
			"server", name,
			"failures", circuit.failures,
			"open_until", circuit.openUntil)
		metrics.MCPToolCalls.WithLabelValues(name, "circuit_breaker", "opened").Inc()
	}
}

// reconnect re-establishes a connection to the specified server by creating a new transport and toolset
func (c *MCPClient) reconnect(name string, logger *slog.Logger) (tool.Toolset, error) {
	c.mu.RLock()
	info, ok := c.endpoints[name]
	c.mu.RUnlock()

	if !ok {
		return nil, fmt.Errorf("mcp server not configured: %s", name)
	}

	logger.Info("connecting")

	// Release old references to allow GC to clean up
	// Note: mcp.Transport does not have Close() - cleanup happens via GC
	// when the old mcptoolset's internal session is garbage collected
	c.mu.Lock()
	delete(c.transports, name)
	delete(c.toolsets, name)
	c.mu.Unlock()

	// Create new transport via factory
	// Use c.baseCtx to bind transport lifecycle to the client, not the request
	transport, err := c.transportFactory(c.baseCtx, info.endpoint, info.token, info.authHeader)
	if err != nil {
		return nil, fmt.Errorf("create transport %s: %w", name, err)
	}

	// Create native toolset - ADK will manage the session lifecycle using this transport
	cfg := mcptoolset.Config{
		Transport: transport,
	}

	// Apply whitelist if configured (empty = all tools allowed)
	if len(info.allowedTools) > 0 {
		cfg.ToolFilter = tool.StringPredicate(info.allowedTools)
		logger.Debug("tool filter applied", "allowed", info.allowedTools)
	}

	ts, err := mcptoolset.New(cfg)
	if err != nil {
		// Transport will be GC'd since we're not storing it
		return nil, fmt.Errorf("create toolset %s: %w", name, err)
	}

	c.mu.Lock()
	c.transports[name] = transport
	c.toolsets[name] = ts
	c.stale[name] = false
	// Reset circuit breaker on success
	delete(c.circuits, name)
	c.mu.Unlock()

	logger.Info("connected")
	return ts, nil
}

// backoff sleeps with exponential backoff, respecting context
func (c *MCPClient) backoff(ctx context.Context, attempt int) {
	backoff := c.cfg.MCP.Retry.Backoff * time.Duration(1<<attempt)
	if backoff > c.cfg.MCP.Retry.MaxBackoff {
		backoff = c.cfg.MCP.Retry.MaxBackoff // Cap at 30s
	}
	select {
	case <-ctx.Done():
		slog.Debug("backoff cancelled")
	case <-time.After(backoff):
	}
}

// forceReconnect forces a reconnection for a server
func (c *MCPClient) forceReconnect(name string) {
	c.mu.Lock()
	c.stale[name] = true
	c.mu.Unlock()
}

// CallTool calls a tool on a specific MCP server with retry logic
func (c *MCPClient) CallTool(ctx context.Context, serverName, toolName string, args map[string]interface{}) (any, error) {
	slog.Debug("call tool", "server", serverName, "tool", toolName)

	// Get initial toolset
	ts, err := c.getOrReconnect(serverName)
	if err != nil {
		return nil, err
	}

	// Find tool
	tCtx := types.NewNopToolContext(ctx, "", "")
	tools, err := ts.Tools(tCtx)
	if err != nil {
		return nil, fmt.Errorf("list tools: %w", err)
	}

	for _, t := range tools {
		if t.Name() == toolName {
			// Wrap with RetryTool which handles retries, reconnections, and backoff
			rt := &RetryTool{
				client:     c,
				serverName: serverName,
				inner:      t,
				toolName:   toolName,
			}
			return rt.Run(tCtx, args)
		}
	}

	return nil, fmt.Errorf("tool %s not found on %s", toolName, serverName)
}

// RetryToolset wraps a native ADK Toolset to add retry logic
type RetryToolset struct {
	client     *MCPClient
	serverName string
}

func (rt *RetryToolset) Name() string {
	return "mcp-" + rt.serverName
}

// Tools fetches tools from the underlying native toolset and wraps them
func (rt *RetryToolset) Tools(ctx agent.ReadonlyContext) ([]tool.Tool, error) {
	// 1. Get Inner Toolset (with retry on fetch)
	var inner tool.Toolset
	var err error

	// Try to ensure connection first
	rt.client.getOrReconnect(rt.serverName) // best effort

	inner, err = rt.client.getNativeToolset(rt.serverName)
	if err != nil {
		return nil, err
	}

	// 2. Call Native Tools
	nativeTools, err := inner.Tools(ctx)
	if err != nil {
		return nil, err
	}

	// 3. Wrap
	wrappedTools := make([]tool.Tool, len(nativeTools))
	for i, t := range nativeTools {
		wrappedTools[i] = &RetryTool{
			client:     rt.client,
			serverName: rt.serverName,
			inner:      t,
			toolName:   t.Name(),
		}
	}

	return wrappedTools, nil
}

// RetryTool wraps a specific tool to retry its execution on failure
type RetryTool struct {
	client     *MCPClient
	serverName string
	inner      tool.Tool
	toolName   string
}

// RunnableTool defines the interface for tools that can run.
// This matches what we expect from ADK's native tools (like functiontool).
type RunnableTool interface {
	Run(ctx tool.Context, args any) (map[string]any, error)
}

// DeclarationProvider allows access to the underlying schema for some tool implementations
type DeclarationProvider interface {
	Declaration() *genai.FunctionDeclaration
}

func (t *RetryTool) Name() string {
	return t.inner.Name()
}

func (t *RetryTool) Description() string {
	return t.inner.Description()
}

func (t *RetryTool) IsLongRunning() bool {
	return t.inner.IsLongRunning()
}

// Declaration implements the interface that ADK might look for to get schema
func (t *RetryTool) Declaration() *genai.FunctionDeclaration {
	if dp, ok := t.inner.(DeclarationProvider); ok {
		return dp.Declaration()
	}
	return nil
}

// ProcessRequest implements toolinternal.RequestProcessor.
// This delegates to the wrapped tool to ensure ADK's preprocessing works correctly.
func (t *RetryTool) ProcessRequest(ctx tool.Context, req *model.LLMRequest) error {
	if rp, ok := t.inner.(interface {
		ProcessRequest(ctx tool.Context, req *model.LLMRequest) error
	}); ok {
		return rp.ProcessRequest(ctx, req)
	}
	return nil // No-op if inner doesn't implement it (fallback)
}

// Run executes the tool with retry logic
func (t *RetryTool) Run(ctx tool.Context, args any) (map[string]any, error) {
	slog.Debug("executing tool", "server", t.serverName, "tool", t.toolName, "args", args)
	maxAttempts := t.client.cfg.MCP.Retry.Attempts
	if maxAttempts <= 0 {
		maxAttempts = 1
	}

	var lastErr error
	// Use local variable to avoid concurrent writes to shared t.inner field
	currentTool := t.inner

	for attempt := range maxAttempts {
		// Check for context cancellation before each attempt
		if err := ctx.Err(); err != nil {
			metrics.MCPToolCalls.WithLabelValues(t.serverName, t.toolName, "cancelled").Inc()
			return nil, err
		}

		// 1. Cast and Run current tool
		runnable, ok := currentTool.(RunnableTool)
		if !ok {
			return nil, fmt.Errorf("tool %s: not runnable", t.toolName)
		}

		result, err := runnable.Run(ctx, args)
		if err == nil {
			metrics.MCPToolCalls.WithLabelValues(t.serverName, t.toolName, "success").Inc()

			// Apply Response Filter
			t.client.mu.RLock()
			filter := t.client.responseFilters[t.serverName]
			t.client.mu.RUnlock()

			if filter != nil {
				// Result is map[string]any, we treat it as any for the filter interface
				filtered := filter.Filter(t.toolName, result)
				if asMap, ok := filtered.(map[string]any); ok {
					return asMap, nil
				}
				// If filter returns something else (shouldn't happen for map), return original
				slog.Warn("response filter returned non-map", "tool", t.toolName, "type", fmt.Sprintf("%T", filtered))
			}

			return result, nil
		}

		lastErr = err

		if attempt == maxAttempts-1 {
			break
		}

		slog.Debug("exec failed, retrying",
			"server", t.serverName,
			"tool", t.toolName,
			"attempt", attempt+1,
			"error", err)

		metrics.MCPToolCalls.WithLabelValues(t.serverName, t.toolName, "retry").Inc()

		// 2. Retry Logic: Reconnect
		t.client.forceReconnect(t.serverName)
		t.client.backoff(ctx, attempt)

		// 3. Refresh Connection & Toolset
		newTs, err := t.client.getOrReconnect(t.serverName)
		if err != nil {
			lastErr = fmt.Errorf("reconnect: %w", err)
			continue
		}

		// 4. Find Tool in New Toolset and update LOCAL variable (not shared field)
		tools, err := newTs.Tools(ctx)
		if err != nil {
			lastErr = fmt.Errorf("list tools: %w", err)
			continue
		}

		found := false
		for _, tool := range tools {
			if tool.Name() == t.toolName {
				currentTool = tool // Update local variable, not t.inner
				found = true
				break
			}
		}

		if !found {
			lastErr = fmt.Errorf("tool %s: not found after refresh", t.toolName)
			continue
		}
	}

	metrics.MCPToolCalls.WithLabelValues(t.serverName, t.toolName, "error").Inc()
	return nil, fmt.Errorf("run %s/%s: %d retries exhausted: %w", t.serverName, t.toolName, maxAttempts, lastErr)
}
