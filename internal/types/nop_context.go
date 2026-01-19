package types

import (
	"context"
	"crypto/rand"
	"encoding/hex"

	"google.golang.org/adk/agent"
	"google.golang.org/adk/memory"
	"google.golang.org/adk/session"
	"google.golang.org/genai"
)

// NopToolContext implements tool.Context for direct tool calls or testing where
// full session context is not required.
type NopToolContext struct {
	context.Context
	prID      string
	requestID string
}

// NewNopToolContext creates a context with optional metadata
func NewNopToolContext(ctx context.Context, prID, requestID string) *NopToolContext {
	if prID == "" {
		prID = "unknown"
	}
	if requestID == "" {
		b := make([]byte, 8)
		rand.Read(b)
		requestID = hex.EncodeToString(b)
	}
	return &NopToolContext{Context: ctx, prID: prID, requestID: requestID}
}

// AgentName returns a default agent name.
func (c *NopToolContext) AgentName() string { return "mcp-client" }

// AppName returns a default app name.
func (c *NopToolContext) AppName() string { return "mcp-client-app" }

// SessionID returns a default session ID using PR ID.
func (c *NopToolContext) SessionID() string { return "pr-" + c.prID }

// ConversationID returns an empty conversation ID.
func (c *NopToolContext) ConversationID() string { return "" }

// UserID returns a default user ID.
func (c *NopToolContext) UserID() string { return "user" }

// Branch returns a default branch.
func (c *NopToolContext) Branch() string { return "main" }

// InvocationID returns the request ID.
func (c *NopToolContext) InvocationID() string { return c.requestID }

// UserContent returns nil for user content.
func (c *NopToolContext) UserContent() *genai.Content { return nil }

// ReadonlyState returns nil for readonly state.
func (c *NopToolContext) ReadonlyState() session.ReadonlyState { return nil }

// Artifacts returns nil for artifacts.
func (c *NopToolContext) Artifacts() agent.Artifacts { return nil }

// State returns nil for session state.
func (c *NopToolContext) State() session.State { return nil }

// FunctionCallID returns a default function call ID.
func (c *NopToolContext) FunctionCallID() string { return "call-id" }

// Actions returns nil for event actions.
func (c *NopToolContext) Actions() *session.EventActions { return nil }

// SearchMemory returns nil for search memory.
func (c *NopToolContext) SearchMemory(ctx context.Context, query string) (*memory.SearchResponse, error) {
	return nil, nil
}
