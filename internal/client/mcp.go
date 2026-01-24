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

	"pr-review-automation/internal/config"
	"pr-review-automation/internal/filter"
)

// TransportFactory creates a new transport
type TransportFactory func(ctx context.Context, endpoint, token, authHeader string, timeout time.Duration) (mcp.Transport, error)

// MCPClient manages connections to MCP servers
type MCPClient struct {
	cfg             *config.Config
	transports      map[string]mcp.Transport
	sessions        map[string]*mcp.ClientSession    // Active MCP sessions
	endpoints       map[string]endpointInfo          // Store endpoint info for reconnection
	stale           map[string]bool                  // Track stale connections
	circuits        map[string]*circuitState         // Circuit breaker state per server
	responseFilters map[string]filter.ResponseFilter // Response filters per server
	callHistory     sync.Map                         // History of tool calls for deduplication

	mu               sync.RWMutex       // Thread-safe access (connections)
	transportFactory TransportFactory   // Factory for creating transports (injectable for testing)
	requestGroup     singleflight.Group // Singleflight group for coalescing reconnections
	baseCtx          context.Context    // Lifecycle context for the client and its transports
	cancel           context.CancelFunc // Cancel function to cleanup resources on Close
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

// NewMCPClient creates a new MCP client manager
func NewMCPClient(cfg *config.Config) *MCPClient {
	ctx, cancel := context.WithCancel(context.Background())
	return &MCPClient{
		cfg:              cfg,
		transports:       make(map[string]mcp.Transport),
		sessions:         make(map[string]*mcp.ClientSession),
		endpoints:        make(map[string]endpointInfo),
		stale:            make(map[string]bool),
		circuits:         make(map[string]*circuitState),
		responseFilters:  make(map[string]filter.ResponseFilter),
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

		// Setup filters from config
		if len(serverCfg.ResponseFilters) > 0 {
			c.mu.Lock()
			existing := c.responseFilters[name]
			// Check if existing is already a chain, or create new chain
			var chain *filter.FilterChain
			if existingChain, ok := existing.(*filter.FilterChain); ok {
				chain = existingChain
			} else {
				chain = filter.NewFilterChain()
				if existing != nil {
					chain.Add(existing)
				}
				c.responseFilters[name] = chain
			}

			for _, fCfg := range serverCfg.ResponseFilters {
				f, err := filter.Create(fCfg.Name, fCfg.Options)
				if err != nil {
					slog.Error("failed to create filter", "filter", fCfg.Name, "error", err)
					continue
				}
				chain.Add(f)
			}
			c.mu.Unlock()
		}

		_, err := c.getOrReconnect(name)
		if err != nil {
			slog.Error("connect mcp failed", "server", name, "error", err)
			return err
		}
		return nil
	}

	addServerConn(config.MCPServerBitbucket, c.cfg.MCP.Bitbucket)
	// Optimization: Only connect if tools are explicitly allowed (enabled)
	if len(c.cfg.MCP.Jira.AllowedTools) > 0 {
		addServerConn(config.MCPServerJira, c.cfg.MCP.Jira)
	}
	if len(c.cfg.MCP.Confluence.AllowedTools) > 0 {
		addServerConn(config.MCPServerConfluence, c.cfg.MCP.Confluence)
	}

	// Pre-fetch and cache capabilities

	return nil
}

// Close releases MCP resources.
func (c *MCPClient) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.cancel != nil {
		slog.Debug("closing mcp client")
		c.cancel()
	}

	var errs []error

	for name, transport := range c.transports {
		if closer, ok := transport.(io.Closer); ok {
			if err := closer.Close(); err != nil {
				slog.Error("close transport failed", "server", name, "error", err)
				errs = append(errs, fmt.Errorf("close %s: %w", name, err))
			} else {
				slog.Debug("transport closed", "server", name)
			}
		}

		delete(c.transports, name)
		delete(c.sessions, name)
	}

	if len(errs) > 0 {
		return fmt.Errorf("close transports: %v", errs)
	}

	return nil
}

// getSession returns the active session for a server
func (c *MCPClient) getSession(name string) (*mcp.ClientSession, error) {
	c.mu.RLock()
	s, ok := c.sessions[name]
	c.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("session not found: %s", name)
	}
	return s, nil
}

// reconnect re-establishes a connection to the specified server
func (c *MCPClient) reconnect(name string, logger *slog.Logger) (*mcp.ClientSession, error) {
	c.mu.RLock()
	info, ok := c.endpoints[name]
	c.mu.RUnlock()

	if !ok {
		return nil, fmt.Errorf("mcp server not configured: %s", name)
	}

	logger.Info("connecting")

	c.mu.Lock()
	delete(c.transports, name)
	delete(c.sessions, name)
	c.mu.Unlock()

	transport, err := c.transportFactory(c.baseCtx, info.endpoint, info.token, info.authHeader, c.cfg.MCP.Timeout)
	if err != nil {
		return nil, fmt.Errorf("create transport %s: %w", name, err)
	}

	// Create MCP Client
	mcpClient := mcp.NewClient(&mcp.Implementation{
		Name:    "agent-sets",
		Version: "1.0.0",
	}, nil)

	// Connect to create a session
	session, err := mcpClient.Connect(c.baseCtx, transport, nil)
	if err != nil {
		return nil, fmt.Errorf("mcp connect %s: %w", name, err)
	}

	c.mu.Lock()
	c.transports[name] = transport
	c.sessions[name] = session
	c.stale[name] = false
	delete(c.circuits, name)
	c.mu.Unlock()

	logger.Info("connected")
	return session, nil
}
