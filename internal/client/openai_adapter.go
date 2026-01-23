package client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"iter"
	"log/slog"
	"pr-review-automation/internal/types"
	"strings"
	"sync"

	"github.com/openai/openai-go"
	"github.com/openai/openai-go/packages/param"
	"github.com/openai/openai-go/shared"
	"github.com/openai/openai-go/shared/constant"
	"google.golang.org/adk/model"
	"google.golang.org/genai"
)

// OpenAIAdapter implements model.LLM interface using OpenAI official client
type OpenAIAdapter struct {
	client   *openai.Client
	model    string
	endpoint string
	apiKey   string
	mu       sync.Mutex
}

// NewOpenAIAdapter creates a new OpenAI adapter
func NewOpenAIAdapter(client *openai.Client, model string) *OpenAIAdapter {
	return &OpenAIAdapter{
		client: client,
		model:  model,
	}
}

// NewOpenAIAdapterWithConfig creates a new OpenAI adapter with endpoint and API key stored
func NewOpenAIAdapterWithConfig(client *openai.Client, model, endpoint, apiKey string) *OpenAIAdapter {
	return &OpenAIAdapter{
		client:   client,
		model:    model,
		endpoint: endpoint,
		apiKey:   apiKey,
	}
}

// Name returns the model name
func (a *OpenAIAdapter) Name() string {
	return "openai-" + a.model
}

// GetConfig returns the endpoint and API key for external use
func (a *OpenAIAdapter) GetConfig() (endpoint, apiKey string) {
	// Extract from client - OpenAI client stores baseURL and API key internally
	// We need to store them in the adapter for this to work
	return a.endpoint, a.apiKey
}

// Ping sends a minimal request to verify connection
func (a *OpenAIAdapter) Ping(ctx context.Context) error {
	slog.Info("checking llm connection...")
	params := openai.ChatCompletionNewParams{
		Model: shared.ChatModel(a.model),
		Messages: []openai.ChatCompletionMessageParamUnion{
			openai.UserMessage("hello"),
		},
		MaxTokens: openai.Int(1),
	}
	_, err := a.client.Chat.Completions.New(ctx, params)
	if err != nil {
		return fmt.Errorf("llm ping failed: %w", err)
	}
	slog.Info("llm connection verified")
	return nil
}

// GenerateContent generates content using OpenAI
func (a *OpenAIAdapter) GenerateContent(ctx context.Context, req *model.LLMRequest, stream bool) iter.Seq2[*model.LLMResponse, error] {
	return func(yield func(*model.LLMResponse, error) bool) {
		a.mu.Lock()
		defer a.mu.Unlock()
		slog.Debug("llm request", "model", a.model, "stream", stream, "messages", len(req.Contents))

		// 1. Convert Messages
		messages, err := a.convertMessages(req.Contents)
		if err != nil {
			yield(nil, fmt.Errorf("convert messages: %w", err))
			return
		}

		// 2. Convert Tools
		tools, err := a.convertTools(req.Tools)
		if err != nil {
			yield(nil, fmt.Errorf("convert tools: %w", err))
			return
		}

		// 3. Prepare Request
		params := openai.ChatCompletionNewParams{
			Model:    shared.ChatModel(a.model),
			Messages: messages,
		}

		// DEBUG logging for 400 error investigation
		if len(messages) == 0 {
			slog.Error("openai request has empty messages", "req_parts", len(req.Contents))
		} else {
			msgsJSON, _ := json.Marshal(messages)
			slog.Debug("openai request messages", "count", len(messages), "payload", string(msgsJSON))
		}
		if len(tools) > 0 {
			params.Tools = tools
		}

		// Handle Configuration
		if req.Config != nil {
			if req.Config.ResponseMIMEType == "application/json" {
				val := shared.NewResponseFormatJSONObjectParam()
				params.ResponseFormat = openai.ChatCompletionNewParamsResponseFormatUnion{
					OfJSONObject: &val,
				}
			}

			// Handle System Instruction
			if req.Config.SystemInstruction != nil {
				sysMsg := ""
				for _, p := range req.Config.SystemInstruction.Parts {
					if p.Text != "" {
						sysMsg += p.Text
					}
				}
				if sysMsg != "" {
					// Prepend system message
					newMessages := make([]openai.ChatCompletionMessageParamUnion, 0, len(messages)+1)
					newMessages = append(newMessages, openai.SystemMessage(sysMsg))
					newMessages = append(newMessages, messages...)
					messages = newMessages
					params.Messages = messages // Update params
				}
			}
		}

		// 4. Execute
		if stream {
			a.handleStream(ctx, params, yield)
		} else {
			a.handleUnary(ctx, params, yield)
		}
	}
}

// handleUnary handles non-streaming requests
func (a *OpenAIAdapter) handleUnary(ctx context.Context, params openai.ChatCompletionNewParams, yield func(*model.LLMResponse, error) bool) {
	resp, err := a.client.Chat.Completions.New(ctx, params)
	if err != nil {
		yield(nil, a.wrapError(fmt.Errorf("openai request: %w", err)))
		return
	}

	if len(resp.Choices) == 0 {
		yield(nil, fmt.Errorf("no openai response"))
		return
	}

	choice := resp.Choices[0]
	slog.Debug("llm response choice", "content", choice.Message.Content, "tool_calls", len(choice.Message.ToolCalls))
	llmResp, err := a.convertResponse(choice.Message)
	if err != nil {
		yield(nil, fmt.Errorf("convert response: %w", err))
		return
	}
	llmResp.TurnComplete = true
	slog.Debug("llm response", "parts", len(llmResp.Content.Parts))
	yield(llmResp, nil)
}

// handleStream handles streaming requests
func (a *OpenAIAdapter) handleStream(ctx context.Context, params openai.ChatCompletionNewParams, yield func(*model.LLMResponse, error) bool) {
	stream := a.client.Chat.Completions.NewStreaming(ctx, params)
	defer stream.Close()

	var accumulatedToolCalls []openai.ChatCompletionChunkChoiceDeltaToolCall
	var contentBuilder strings.Builder

	for stream.Next() {
		chunk := stream.Current()
		if len(chunk.Choices) == 0 {
			continue
		}
		delta := chunk.Choices[0].Delta

		// Handle Text Content
		if delta.Content != "" {
			contentBuilder.WriteString(delta.Content)
			part := genai.NewPartFromText(delta.Content)
			if !yield(&model.LLMResponse{ // Fixed: Parts expects []*genai.Part
				Content: &genai.Content{
					Parts: []*genai.Part{part},
					Role:  "model",
				},
				Partial: true,
			}, nil) {
				return
			}
		}

		// Handle Tool Calls (Accumulate them)
		if len(delta.ToolCalls) > 0 {
			accumulateToolCalls(&accumulatedToolCalls, delta.ToolCalls)
		}
	}

	if err := stream.Err(); err != nil {
		yield(nil, a.wrapError(fmt.Errorf("stream: %w", err)))
		return
	}

	// If we had tool calls, yield them as a final message
	if len(accumulatedToolCalls) > 0 {
		var parts []*genai.Part // Fixed: slice of pointers
		for _, tc := range accumulatedToolCalls {
			args := make(map[string]any)
			if tc.Function.Arguments != "" {
				if err := json.Unmarshal([]byte(tc.Function.Arguments), &args); err != nil {
					slog.Warn("parse args failed", "error", err)
				}
			}
			part := genai.NewPartFromFunctionCall(tc.Function.Name, args)
			parts = append(parts, part) // Fixed: append pointer directly
		}
		yield(&model.LLMResponse{
			Content: &genai.Content{
				Parts: parts,
				Role:  "model",
			},
			TurnComplete: true,
		}, nil)
		return
	}

	// Send final turn complete signal
	yield(&model.LLMResponse{TurnComplete: true}, nil)
}

// Helper to accumulate delta tool calls
func accumulateToolCalls(acc *[]openai.ChatCompletionChunkChoiceDeltaToolCall, deltas []openai.ChatCompletionChunkChoiceDeltaToolCall) {
	for _, d := range deltas {
		// Find existing tool call with same index
		found := false
		for i := range *acc {
			if (*acc)[i].Index == d.Index {
				// Only accumulate Arguments; Name and ID are set once on first occurrence
				(*acc)[i].Function.Arguments += d.Function.Arguments
				if (*acc)[i].Function.Name == "" && d.Function.Name != "" {
					(*acc)[i].Function.Name = d.Function.Name
				}
				if (*acc)[i].ID == "" && d.ID != "" {
					(*acc)[i].ID = d.ID
				}
				found = true
				break
			}
		}
		if !found {
			*acc = append(*acc, d)
		}
	}
}

// convertMessages converts ADK (genai) messages to OpenAI messages
func (a *OpenAIAdapter) convertMessages(contents []*genai.Content) ([]openai.ChatCompletionMessageParamUnion, error) {
	var messages []openai.ChatCompletionMessageParamUnion
	for _, c := range contents {
		var role string
		switch c.Role {
		case "user":
			role = "user"
		case "model":
			role = "assistant"
		case "tool":
			role = "tool" // Special handling for tool response
		default:
			role = "user"
		}

		// DEBUG: Log role and parts info
		slog.Debug("convertMessages", "original_role", c.Role, "mapped_role", role, "parts_count", len(c.Parts))

		var textParts []string
		var toolCallParts []*genai.FunctionCall
		var toolResponseParts []*genai.FunctionResponse

		for _, p := range c.Parts {
			if t := p.Text; t != "" {
				textParts = append(textParts, t)
			}
			if fc := p.FunctionCall; fc != nil {
				toolCallParts = append(toolCallParts, fc)
			}
			if fr := p.FunctionResponse; fr != nil {
				toolResponseParts = append(toolResponseParts, fr)
			}
		}

		text := ""
		if len(textParts) > 0 {
			text = strings.Join(textParts, "\n")
		}

		switch role {
		case "user":
			// ADK wraps tool responses as "user" role with FunctionResponse parts
			// Check for tool responses first
			if len(toolResponseParts) > 0 {
				for _, tr := range toolResponseParts {
					contentJSON, err := json.Marshal(tr.Response)
					if err != nil {
						slog.Warn("marshal response failed", "name", tr.Name, "error", err)
						contentJSON = []byte("{}")
					}
					toolCallID := tr.ID
					if toolCallID == "" {
						toolCallID = "call_" + tr.Name
					}
					if toolCallID != "" {
						messages = append(messages, openai.ToolMessage(string(contentJSON), toolCallID))
					}
				}
			} else {
				// Regular user message
				messages = append(messages, openai.UserMessage(text))
			}
		case "assistant":
			// Assistant message can have text AND tool calls
			msg := openai.AssistantMessage(text)

			if len(toolCallParts) > 0 {
				var tools []openai.ChatCompletionMessageToolCallParam
				for _, tc := range toolCallParts {
					argsJSON, err := json.Marshal(tc.Args)
					if err != nil {
						slog.Warn("marshal args failed", "name", tc.Name, "error", err)
						argsJSON = []byte("{}")
					}
					// Use original ID from genai.FunctionCall if available
					toolCallID := tc.ID
					if toolCallID == "" {
						toolCallID = "call_" + tc.Name // Fallback for legacy compatibility
					}
					tools = append(tools, openai.ChatCompletionMessageToolCallParam{
						ID:   toolCallID,
						Type: constant.Function("function"),
						Function: openai.ChatCompletionMessageToolCallFunctionParam{
							Name:      tc.Name,
							Arguments: string(argsJSON),
						},
					})
				}
				if msg.OfAssistant != nil { // Safety check
					msg.OfAssistant.ToolCalls = tools
				}
			}
			messages = append(messages, msg)

		case "tool":
			for _, tr := range toolResponseParts {
				contentJSON, err := json.Marshal(tr.Response)
				if err != nil {
					slog.Warn("marshal response failed", "name", tr.Name, "error", err)
					contentJSON = []byte("{}")
				}
				// Use original ID from genai.FunctionResponse if available
				toolCallID := tr.ID
				if toolCallID == "" {
					toolCallID = "call_" + tr.Name // Fallback for legacy compatibility
				}
				// OpenAI ToolMessage requires content to be a string.
				// If contentJSON is empty object "{}", strictly speaking it's valid JSON.
				// Ensure ToolCallID is present.
				if toolCallID == "" {
					slog.Warn("missing tool call id in tool response", "name", tr.Name)
					continue
				}
				messages = append(messages, openai.ToolMessage(string(contentJSON), toolCallID))
			}
		}
	}
	return messages, nil
}

// convertTools converts ADK tool definitions to OpenAI tools
func (a *OpenAIAdapter) convertTools(toolsMap map[string]any) ([]openai.ChatCompletionToolParam, error) {
	var openaiTools []openai.ChatCompletionToolParam
	for _, toolVal := range toolsMap {
		// Try to extract declaration from various types
		switch v := toolVal.(type) {
		case *genai.Tool:
			// Handle standard genai.Tool (which contains FunctionDeclarations)
			for _, fd := range v.FunctionDeclarations {
				addFunction(fd, &openaiTools)
			}
			continue
		case genai.Tool:
			for _, fd := range v.FunctionDeclarations {
				addFunction(fd, &openaiTools)
			}
			continue
		case interface {
			Declaration() *genai.FunctionDeclaration
		}:
			// Handle generic tool implementing Declaration() (like mcptoolset.mcpTool or RetryTool)
			fd := v.Declaration()
			if fd != nil {
				addFunction(fd, &openaiTools)
			}
			continue
		default:
			slog.Warn("unknown tool type", "type", fmt.Sprintf("%T", toolVal), "value", toolVal)
			continue
		}
	}
	return openaiTools, nil
}

func addFunction(fd *genai.FunctionDeclaration, tools *[]openai.ChatCompletionToolParam) {
	paramsJSON, err := json.Marshal(fd.Parameters)
	if err != nil {
		slog.Warn("marshal params failed", "name", fd.Name, "error", err)
		paramsJSON = []byte("{}")
	}
	var paramsMap map[string]interface{}
	_ = json.Unmarshal(paramsJSON, &paramsMap)

	*tools = append(*tools, openai.ChatCompletionToolParam{
		Type: constant.Function("function"),
		Function: shared.FunctionDefinitionParam{
			Name:        fd.Name,
			Description: param.NewOpt(fd.Description),
			Parameters:  shared.FunctionParameters(paramsMap),
		},
	})
}

// convertResponse converts OpenAI response to ADK response
func (a *OpenAIAdapter) convertResponse(msg openai.ChatCompletionMessage) (*model.LLMResponse, error) {
	var parts []*genai.Part

	if msg.Content != "" {
		parts = append(parts, &genai.Part{Text: msg.Content})
	}

	for _, tc := range msg.ToolCalls {
		var args map[string]any
		if err := json.Unmarshal([]byte(tc.Function.Arguments), &args); err != nil {
			return nil, fmt.Errorf("parse tool args: %w", err)
		}
		parts = append(parts, &genai.Part{
			FunctionCall: &genai.FunctionCall{
				Name: tc.Function.Name,
				Args: args,
				ID:   tc.ID, // Preserve OpenAI Tool Call ID
			},
		})
	}

	return &model.LLMResponse{
		Content: &genai.Content{
			Parts: parts,
			Role:  "model",
		},
	}, nil
}

// Helper to consolidate tool call deltas

// SimpleTextQuery sends a single text request and returns the text response.
// Ideal for simple Q&A like JSON parsing.
func (a *OpenAIAdapter) SimpleTextQuery(ctx context.Context, systemPrompt, userInput string) (string, error) {
	messages := []openai.ChatCompletionMessageParamUnion{}

	if systemPrompt != "" {
		messages = append(messages, openai.SystemMessage(systemPrompt))
	}
	messages = append(messages, openai.UserMessage(userInput))

	params := openai.ChatCompletionNewParams{
		Model:    shared.ChatModel(a.model),
		Messages: messages,
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	resp, err := a.client.Chat.Completions.New(ctx, params)
	if err != nil {
		return "", a.wrapError(fmt.Errorf("openai simple request: %w", err))
	}

	if len(resp.Choices) == 0 {
		return "", fmt.Errorf("no openai response")
	}

	return resp.Choices[0].Message.Content, nil
}

// wrapError wraps openai errors into RetryableError if applicable
func (a *OpenAIAdapter) wrapError(err error) error {
	if err == nil {
		return nil
	}

	// Check for openai.APIError
	var apiErr *openai.Error
	if errors.As(err, &apiErr) {
		statusCode := apiErr.StatusCode
		// 429 (Rate Limit) and 5xx (Server Errors) are retryable
		if statusCode == 429 || (statusCode >= 500 && statusCode < 600) {
			return types.NewRetryableError(err)
		}
	}

	return err
}
