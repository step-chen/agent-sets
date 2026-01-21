package client

import (
	"context"
	"testing"
)

func TestNewMCPTransport_Stdio(t *testing.T) {
	transport, err := NewMCPTransport(context.Background(), "stdio://echo hello", "", "")
	if err != nil {
		t.Fatalf("NewMCPTransport failed: %v", err)
	}
	if transport == nil {
		t.Fatal("Expected transport to be non-nil")
	}
	// Cannot easily verify internal type without reflection or interface check,
	// but success implies it worked.
}

func TestNewMCPTransport_SSE(t *testing.T) {
	transport, err := NewMCPTransport(context.Background(), "http://localhost:8080/sse", "token", "")
	if err != nil {
		t.Fatalf("NewMCPTransport failed: %v", err)
	}
	if transport == nil {
		t.Fatal("Expected transport to be non-nil")
	}
}

func TestNewMCPTransport_Invalid(t *testing.T) {
	_, err := NewMCPTransport(context.Background(), "invalid://endpoint", "", "")
	if err == nil {
		t.Fatal("Expected error for invalid scheme")
	}
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
