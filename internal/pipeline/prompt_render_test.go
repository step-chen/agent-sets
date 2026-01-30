package pipeline

import (
	"fmt"
	"path/filepath"
	"pr-review-automation/internal/config"
	"testing"
	// "pr-review-automation/internal/types"
	// Assuming types are in pipeline package or imported
)

// MockPromptLoader needed or use real one with correct path
func TestStage3_RenderPrompt_WithRules(t *testing.T) {
	// Setup Prompts Config
	baseDir, _ := filepath.Abs("../../prompts")
	pipelineCfg := &config.PipelineConfig{
		Stage3Review: config.Stage3Config{
			PromptTemplate: "pipeline/stage3",
		},
	}

	loader := NewPromptLoader(baseDir)

	// Config needs to be used
	s := &Stage3{
		cfg:          pipelineCfg,
		promptLoader: loader,
	}

	// Case: Mixed Go, C++, SQL
	changes := []FileChange{
		{Path: "main.go"},
		{Path: "legacy.cpp"},
		{Path: "query.sql"},
	}

	// 1. Test Rule Loading string
	lRules, lNames := s.loadLanguageRules(changes)
	fmt.Printf("Detected Languages: %s\n", lNames)
	fmt.Printf("--- Loaded Rules Content ---\n%s\n----------------------------\n", lRules)

	// 2. Render Full Prompt
	data := map[string]interface{}{
		"LanguageRules": lRules,
		"Language":      lNames,
		"PR": map[string]string{
			"Title":       "Test PR",
			"Description": "Testing prompt injection",
		},
		"Changes":      changes,
		"Context":      []string{},
		"ResultFormat": "JSON_FORMAT_PLACEHOLDER",
	}

	prompt, err := loader.LoadPrompt(pipelineCfg.Stage3Review.PromptTemplate, data)
	if err != nil {
		t.Fatalf("Failed to render prompt: %v", err)
	}

	fmt.Println("\n=== FINAL RENDERED PROMPT (Snippet) ===")
	// Print the section around Instructions to verify injection
	// Only print first 1500 chars to avoid huge log
	if len(prompt) > 1500 {
		fmt.Println(prompt[:1500] + "\n... (truncated)")
	} else {
		fmt.Println(prompt)
	}
}
