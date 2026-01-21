package client

import (
	"testing"

	"google.golang.org/genai"
)

type mockTool struct {
	name string
}

func (m *mockTool) Declaration() *genai.FunctionDeclaration {
	return &genai.FunctionDeclaration{
		Name:        m.name,
		Description: "Mock tool description",
		Parameters: &genai.Schema{
			Type: genai.TypeObject,
			Properties: map[string]*genai.Schema{
				"arg": {Type: genai.TypeString},
			},
		},
	}
}

func TestOpenAIAdapter_ConvertTools_CustomInterface(t *testing.T) {
	adapter := &OpenAIAdapter{}

	// Case 1: genai.Tool
	gTool := genai.Tool{
		FunctionDeclarations: []*genai.FunctionDeclaration{
			{
				Name:        "genai_tool",
				Description: "Standard tool",
				Parameters:  &genai.Schema{Type: genai.TypeObject},
			},
		},
	}

	// Case 2: Custom Tool with Declaration()
	cTool := &mockTool{name: "custom_tool"}

	tools := map[string]any{
		"standard": &gTool,
		"custom":   cTool,
	}

	converted, err := adapter.convertTools(tools)
	if err != nil {
		t.Fatalf("convertTools failed: %v", err)
	}

	if len(converted) != 2 {
		t.Fatalf("expected 2 tools, got %d", len(converted))
	}

	// Verify tool names exist in result
	foundStandard := false
	foundCustom := false

	for _, ct := range converted {
		switch ct.Function.Name {
		case "genai_tool":
			foundStandard = true
		case "custom_tool":
			foundCustom = true
		}
	}

	if !foundStandard {
		t.Error("standard tool not found in converted tools")
	}
	if !foundCustom {
		t.Error("custom tool not found in converted tools")
	}
}
