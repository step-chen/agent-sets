package client

import (
	"context"
	"fmt"
	"net/http"
	"os/exec"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// TokenRoundTripper wraps http.RoundTripper to inject Authorization header
type TokenRoundTripper struct {
	Base       http.RoundTripper
	Token      string
	AuthHeader string
}

// RoundTrip implements http.RoundTripper
func (t *TokenRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	if t.Token != "" {
		if t.AuthHeader != "" {
			req.Header.Set(t.AuthHeader, t.Token)
		} else {
			req.Header.Set("Authorization", "Bearer "+t.Token)
		}
	}
	if t.Base == nil {
		return http.DefaultTransport.RoundTrip(req)
	}
	return t.Base.RoundTrip(req)
}

// NewMCPTransport creates mcp.Transport based on endpoint and token.
// Supports stdio:// and http(s):// schemes.
func NewMCPTransport(ctx context.Context, endpoint, token, authHeader string, timeout time.Duration) (mcp.Transport, error) {
	switch {
	case strings.HasPrefix(endpoint, "stdio://"):
		return newStdioTransport(ctx, endpoint, token)
	case strings.HasPrefix(endpoint, "http://"), strings.HasPrefix(endpoint, "https://"):
		return newSSETransport(ctx, endpoint, token, authHeader, timeout)
	default:
		return nil, fmt.Errorf("unsupported endpoint scheme: %s", endpoint)
	}
}

func newStdioTransport(ctx context.Context, endpoint, token string) (mcp.Transport, error) {
	// Format: stdio://command arg1 arg2
	cmdLine := strings.TrimPrefix(endpoint, "stdio://")
	parts := splitWithQuotes(cmdLine)
	if len(parts) == 0 {
		return nil, fmt.Errorf("invalid stdio endpoint: %s", endpoint)
	}

	cmd := exec.CommandContext(ctx, parts[0], parts[1:]...)
	if token != "" {
		cmd.Env = append(cmd.Environ(), "MCP_TOKEN="+token)
	}
	return &mcp.CommandTransport{Command: cmd}, nil
}

func newSSETransport(_ context.Context, endpoint, token, authHeader string, timeout time.Duration) (mcp.Transport, error) {
	var httpClient *http.Client
	if token != "" {
		httpClient = &http.Client{
			Transport: &TokenRoundTripper{
				Base:       http.DefaultTransport,
				Token:      token,
				AuthHeader: authHeader,
			},
			Timeout: timeout,
		}
	} else {
		// Even without token, we should set timeout
		httpClient = &http.Client{
			Timeout: timeout,
		}
	}
	return &mcp.SSEClientTransport{
		Endpoint:   endpoint,
		HTTPClient: httpClient,
	}, nil
}

func splitWithQuotes(s string) []string {
	var args []string
	var current []rune
	inQuote := false
	quoteChar := rune(0)

	for _, c := range s {
		if inQuote {
			if c == quoteChar {
				inQuote = false
			} else {
				current = append(current, c)
			}
		} else {
			switch c {
			case '"', '\'':
				inQuote = true
				quoteChar = c
			case ' ', '\t':
				if len(current) > 0 {
					args = append(args, string(current))
					current = nil
				}
			default:
				current = append(current, c)
			}
		}
	}
	if len(current) > 0 {
		args = append(args, string(current))
	}
	return args
}
