package pipeline

import (
	"strings"
	"testing"

	codecontext "pr-review-automation/internal/context"

	"github.com/stretchr/testify/assert"
)

func TestContextReducer_Reduce(t *testing.T) {
	reducer := NewContextReducer() // Small budget via Reduce arg

	// Case 1: Within budget
	files := []FileContent{
		{Path: "small.go", Content: "package main\nfunc main() {}", IsDiffed: false},
	}
	reduced := reducer.Reduce(files, 100)
	assert.Equal(t, 1, len(reduced))
	assert.Equal(t, "package main\nfunc main() {}", reduced[0].Content)

	// Case 2: Exceeds budget, no chunks -> Truncate (Fallback)
	largeContent := strings.Repeat("line\n", 50) // 5 chars * 50 = 250 chars ~ 83 tokens.
	// Budget 100. It fits? Wait. 83 < 100.
	// Reduce budget to force truncation.
	// Let's use 200 lines -> 1000 chars ~ 333 tokens.
	largeContent = strings.Repeat("line\n", 200)

	files = []FileContent{
		{Path: "large.txt", Content: largeContent, IsDiffed: false},
	}
	reduced = reducer.Reduce(files, 50) // Budget 50 ~ 150 chars ~ 30 lines
	assert.Equal(t, 1, len(reduced), "Should keep truncated file")
	assert.Contains(t, reduced[0].Content, "truncated remaining")

	// Case 3: Exceeds budget, has chunks -> Compress
	// Content: function with large body
	funcBody := strings.Repeat("statement();\n", 50)
	src := "func MyFunc() {\n" + funcBody + "}"
	// Chunks info
	chunks := []codecontext.Chunk{
		{Name: "MyFunc", Type: "function", StartLine: 1, EndLine: 52},
	}

	files = []FileContent{
		{
			Path:     "code.go",
			Content:  src,
			IsDiffed: false,
			Analysis: &codecontext.FileAnalysis{Chunks: chunks},
		},
	}

	// Budget 50. Content ~50 lines * 10 chars = 500 chars / 3 = 160 tokens.
	// Compressed: func MyFunc() {\n ... } ~20 chars = 7 tokens.

	reduced = reducer.Reduce(files, 50)
	assert.Equal(t, 1, len(reduced))
	assert.Contains(t, reduced[0].Content, "implementation hidden")
	assert.Contains(t, reduced[0].Content, "func MyFunc() {")
}

func TestContextReducer_DiffPriority(t *testing.T) {
	reducer := NewContextReducer()

	files := []FileContent{
		{Path: "context.go", Content: strings.Repeat("a\n", 50), IsDiffed: false}, // 50 lines. ~100 chars. ~28 tokens.
		{Path: "diff.go", Content: strings.Repeat("b\n", 50), IsDiffed: true},     // ~28 tokens.
	}
	// Total 66 > 50.
	// Should keep diff.go (33). Remaining 17.
	// context.go (33) > 17. Drop.

	reduced := reducer.Reduce(files, 50)
	// With new logic, we truncate context.go instead of dropping it if it fits >= 20 tokens
	// 50 - 28 (diff) = 22 remaining. 22 > 20. So we keep truncated context.
	assert.Equal(t, 2, len(reduced))
	assert.Equal(t, "diff.go", reduced[0].Path)
	assert.Equal(t, "context.go", reduced[1].Path)
	assert.Contains(t, reduced[1].Content, "truncated")
}
