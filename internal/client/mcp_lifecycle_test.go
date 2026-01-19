package client_test

import (
	"context"
	"testing"
	"time"

	"pr-review-automation/internal/client"
	"pr-review-automation/internal/config"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// MockTransport implements mcp.Transport
type MockTransport struct {
	closed bool
}

func (t *MockTransport) Start(ctx context.Context) error {
	// Simulate blocking until context is done
	<-ctx.Done()
	return ctx.Err()
}

// Check what Send signature is needed by compiler
func (t *MockTransport) Send(ctx context.Context, message any) (any, error) {
	return nil, nil
}

func (t *MockTransport) Close() error {
	t.closed = true
	return nil
}

// Connect implements missing method
func (t *MockTransport) Connect(ctx context.Context) (mcp.Connection, error) {
	return &MockConnection{}, nil
}

// MockConnection implements mcp.Connection
type MockConnection struct{}

// Send implements mcp.Connection.Send
func (c *MockConnection) Send(ctx context.Context, message jsonrpc.Message) (jsonrpc.Message, error) {
	return nil, nil
}

func (c *MockConnection) Close() error {
	return nil
}

// Read implements missing method
func (c *MockConnection) Read(ctx context.Context) (jsonrpc.Message, error) {
	return nil, nil
}

// Write implements missing method
func (c *MockConnection) Write(ctx context.Context, message jsonrpc.Message) error {
	return nil
}

// SessionID implements missing method
func (c *MockConnection) SessionID() string {
	return "mock-session-id"
}

func TestMCPClient_Lifecycle_Decoupling(t *testing.T) {
	// 1. Setup config (using anonymous structs as per config.go definition)
	cfg := &config.Config{}
	cfg.MCP.Bitbucket.Endpoint = "mock://bitbucket"
	cfg.MCP.Bitbucket.Token = "token"

	client := client.NewMCPClient(cfg)

	// Mock transport factory
	transportCtxChan := make(chan context.Context, 1)
	client.SetTransportFactory(func(ctx context.Context, endpoint, token string) (mcp.Transport, error) {
		transportCtxChan <- ctx
		return &MockTransport{}, nil
	})

	// 2. Initialize with a SHORT-LIVED request context
	reqCtx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	err := client.InitializeConnections()
	if err != nil {
		t.Fatalf("InitializeConnections failed: %v", err)
	}

	// 3. Capture the context passed to the transport
	var transportCtx context.Context
	select {
	case transportCtx = <-transportCtxChan:
	default:
		t.Fatal("Transport factory was not called")
	}

	// 4. Wait for the REQUEST context to expire
	<-reqCtx.Done()
	time.Sleep(100 * time.Millisecond) // Give it a moment to potentially propagate

	// 5. Verify the TRANSPORT context is STILL ALIVE
	if transportCtx.Err() != nil {
		t.Errorf("Transport context should stay alive after request context expires, but it error: %v", transportCtx.Err())
	}

	// 6. Close the client
	client.Close()

	// 7. Verify the TRANSPORT context is NOW CANCELLED
	if transportCtx.Err() == nil {
		t.Error("Transport context should be cancelled after Client.Close()")
	}
}
