package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"pr-review-automation/internal/types"
)

type MockRawSchemaProvider struct {
	Schemas map[string][]types.RawToolSchema
}

func (m *MockRawSchemaProvider) GetRawToolSchemas() map[string][]types.RawToolSchema {
	return m.Schemas
}

func TestPromptLoader_SchemaInjection(t *testing.T) {
	// 1. Setup temp dir and prompt file
	tempDir := t.TempDir()
	defaultDir := filepath.Join(tempDir, "default")
	if err := os.MkdirAll(defaultDir, 0755); err != nil {
		t.Fatalf("mkdir prompt dir: %v", err)
	}

	// Prompt using the tool variable
	promptContent := "Tool: {{.ToolBitbucketGetDiff}}"
	if err := os.WriteFile(filepath.Join(defaultDir, "default.md"), []byte(promptContent), 0644); err != nil {
		t.Fatalf("write prompt: %v", err)
	}

	loader := NewPromptLoader(tempDir)

	// 2. Verify default behavior (without schema)
	// Expecting just the tool name
	got, err := loader.Load("default", "default", nil)
	if err != nil {
		t.Fatalf("Load (default) failed: %v", err)
	}
	if !strings.Contains(got, "bitbucket_get_pull_request_diff") || strings.Contains(got, "(") {
		t.Errorf("Expected raw tool name, got: %q", got)
	}

	// 3. Setup Schema Provider with mock schema
	mock := &MockRawSchemaProvider{
		Schemas: map[string][]types.RawToolSchema{
			"bitbucket": {
				{
					Name: "bitbucket_get_pull_request_diff",
					InputSchema: map[string]interface{}{
						"type": "object",
						"properties": map[string]interface{}{
							"projectKey": map[string]interface{}{"type": "string"},
							"repoSlug":   map[string]interface{}{"type": "string"},
							"id":         map[string]interface{}{"type": "integer"},
						},
					},
				},
			},
		},
	}
	loader.SetRawSchemaProvider(mock)

	// 4. Verify enriched behavior
	// Expecting signature: bitbucket_get_pull_request_diff(projectKey, repoSlug, id)
	got, err = loader.Load("default", "default", nil)
	if err != nil {
		t.Fatalf("Load (enriched) failed: %v", err)
	}

	wantParts := []string{
		"bitbucket_get_pull_request_diff(",
		"projectKey",
		"repoSlug",
		"id",
		")",
	}
	for _, part := range wantParts {
		if !strings.Contains(got, part) {
			t.Errorf("Enriched prompt missing part %q, got: %q", part, got)
		}
	}
}
