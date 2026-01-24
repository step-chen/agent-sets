package client

import (
	"context"
	"testing"
	"time" // Added for time.Second

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestNewMCPTransport(t *testing.T) { // Renamed and restructured
	// Test stdio scheme
	t.Run("stdio scheme", func(t *testing.T) {
		t.Parallel()
		// We use "echo" as a command that exists
		transport, err := NewMCPTransport(context.Background(), "stdio://echo", "", "", 30*time.Second)
		if err != nil {
			t.Fatalf("NewMCPTransport failed: %v", err)
		}
		if _, ok := transport.(*mcp.CommandTransport); !ok {
			t.Errorf("expected CommandTransport, got %T", transport)
		}
	})

	// Test http scheme (partially mocked as we don't start server here, just check type)
	t.Run("http scheme", func(t *testing.T) {
		t.Parallel()
		transport, err := NewMCPTransport(context.Background(), "http://localhost:8080/sse", "token", "header", 30*time.Second)
		if err != nil {
			t.Fatalf("NewMCPTransport failed: %v", err)
		}
		if _, ok := transport.(*mcp.SSEClientTransport); !ok {
			t.Errorf("expected SSEClientTransport, got %T", transport)
		}
	})

	// Test unsupported scheme
	t.Run("unsupported scheme", func(t *testing.T) {
		t.Parallel()
		_, err := NewMCPTransport(context.Background(), "ftp://localhost", "", "", 30*time.Second)
		if err == nil {
			t.Error("expected error for unsupported scheme, got nil")
		}
	})
}

func TestSplitWithQuotes(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected []string
	}{
		{"Simple", "echo hello", []string{"echo", "hello"}},
		{"Quotes", "echo 'hello world'", []string{"echo", "hello world"}},
		{"Mixed", `echo "hello" 'world'`, []string{"echo", "hello", "world"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := splitWithQuotes(tt.input)
			if len(got) != len(tt.expected) {
				t.Fatalf("Expected %d args, got %d", len(tt.expected), len(got))
			}
			for i, v := range got {
				if v != tt.expected[i] {
					t.Errorf("Arg %d: expected %q, got %q", i, tt.expected[i], v)
				}
			}
		})
	}
}
