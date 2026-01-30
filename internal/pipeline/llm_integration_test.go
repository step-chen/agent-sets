package pipeline

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"pr-review-automation/internal/client"
	"pr-review-automation/internal/config"
	"pr-review-automation/internal/domain"

	"github.com/joho/godotenv"
	"github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
)

// TestStage3_LLM_Direct verifies the LLM interaction directly without full E2E overhead.
// It sends a crafted PR with C++ violations to check if the new PROMPTS trigger correct reviews.
func TestStage3_LLM_Direct(t *testing.T) {
	// 1. Setup Config
	// Load .env from root
	rootDir, _ := filepath.Abs("../../")
	_ = godotenv.Load(filepath.Join(rootDir, ".env"))

	// Load Config
	os.Setenv("CONFIG_PATH", filepath.Join(rootDir, "test/e2e/config.test.yaml")) // Use test config from correct dir
	appCfg := config.LoadConfig()

	// Try to get API Key via config or env
	apiKey := appCfg.LLM.APIKey
	if apiKey == "" {
		apiKey = os.Getenv("LLM_API_KEY")
	}

	if apiKey == "" {
		t.Skip("Skipping integration test: LLM_API_KEY not set in Config or Env")
	}

	baseDir, _ := filepath.Abs("../../prompts")
	cfg := &config.PipelineConfig{
		Stage3Review: config.Stage3Config{
			PromptTemplate:   "pipeline/stage3",
			Temperature:      0.0,
			MaxContextTokens: 4000,
		},
	}

	// 2. Setup Dependencies
	promptLoader := NewPromptLoader(baseDir)

	// Real OpenAI Client
	opts := []option.RequestOption{
		option.WithAPIKey(apiKey),
	}
	// Use Endpoint from config if available (critical for custom LLM like glm-4)
	if appCfg.LLM.Endpoint != "" {
		opts = append(opts, option.WithBaseURL(appCfg.LLM.Endpoint))
	}

	openaiClient := openai.NewClient(opts...)
	llmClient := client.NewOpenAIAdapter(&openaiClient, appCfg.LLM.Model) // Use configured model too

	// Mock Stage3 (No MCP needed for this direct test if we Mock context)
	// We pass nil for MCP client as we won't call loadContext (we provide it manually)
	stage3 := NewStage3(cfg, nil, llmClient, promptLoader)

	// 3. Construct Review Request with C++ Bad Practices
	// - Use of `new` (Violation of Resource Safety)
	// - Use of `boost::filesystem` (Violation of Modern C++)
	// - Loop instead of algorithm (Violation of Modern C++ ranges)
	_ = `
#include <iostream>
#include <vector>
#include <boost/filesystem.hpp> // Legacy

void process_files() {
    std::vector<int>* numbers = new std::vector<int>(); // Violation: Raw pointer
    numbers->push_back(1);
    numbers->push_back(2);

    // Violation: Manual loop instead of range-based for or algorithm
    for (size_t i = 0; i < numbers->size(); ++i) {
        std::cout << (*numbers)[i] << std::endl;
    }
}
` // kept for reference, real content in FileChange below

	changes := []FileChange{
		{
			Path: "src/legacy_processor.cpp",
			HunkLines: []string{
				"@@ -0,0 +1,15 @@",
				"+#include <iostream>",
				"+#include <vector>",
				"+#include <boost/filesystem.hpp> // Legacy",
				"+",
				"+void process_files() {",
				"+    std::vector<int>* numbers = new std::vector<int>(); // Violation: Raw pointer",
				"+    numbers->push_back(1);",
				"+    numbers->push_back(2);",
				"+",
				"+    // Violation: Manual loop instead of range-based for or algorithm",
				"+    for (size_t i = 0; i < numbers->size(); ++i) {",
				"+        std::cout << (*numbers)[i] << std::endl;",
				"+    }",
				"+}",
			},
		},
	}

	contextFiles := []FileContent{} // No extra context needed for this detection

	req := ReviewRequest{
		PR: domain.PullRequest{
			ID:          "1",
			Title:       "Add processor",
			Description: "Adding a new file processor module",
			ProjectKey:  "TEST",
			RepoSlug:    "test-repo",
		},
	}

	// 4. Run Review
	fmt.Println("Sending request to LLM...")
	start := time.Now()
	// We call reviewCore directly or via Review. Review calls loadLanguageRules internally which we want to test.
	result, err := stage3.Review(context.Background(), req, changes, contextFiles)
	if err != nil {
		t.Fatalf("Review failed: %v", err)
	}
	duration := time.Since(start)

	// 5. Output Results
	fmt.Printf("\n=== LLM Review Completed in %v ===\n", duration)
	fmt.Printf("Score: %d\n", result.Score)
	fmt.Printf("Summary: %s\n", result.Summary)
	fmt.Println("\n--- Inline Comments ---")

	jsonBytes, _ := json.MarshalIndent(result.Comments, "", "  ")
	fmt.Println(string(jsonBytes))

	// 6. Verification Assertions
	foundFilesystem := false
	foundRawPointer := false
	foundRange := false

	for _, c := range result.Comments {
		msg := c.Comment
		if containsIgnoreCase(msg, "filesystem") || containsIgnoreCase(msg, "boost") {
			foundFilesystem = true
		}
		if containsIgnoreCase(msg, "smart pointer") || containsIgnoreCase(msg, "unique_ptr") || containsIgnoreCase(msg, "shared_ptr") || containsIgnoreCase(msg, "RAII") {
			foundRawPointer = true
		}
		if containsIgnoreCase(msg, "range") || containsIgnoreCase(msg, "span") || containsIgnoreCase(msg, "foreach") {
			foundRange = true
		}
	}

	if !foundFilesystem {
		t.Error("Expected comment about std::filesystem vs boost")
	}
	if !foundRawPointer {
		t.Error("Expected comment about smart pointers/RAII")
	}
	// Range is optional but good to check
	if foundRange {
		fmt.Println("SUCCESS: Detected manual loop issue (Ranges rule active)")
	} else {
		fmt.Println("NOTE: Did not detect manual loop issue (Acceptable, maybe not critical)")
	}
}

func containsIgnoreCase(s, substr string) bool {
	return strings.Contains(strings.ToLower(s), strings.ToLower(substr))
}
