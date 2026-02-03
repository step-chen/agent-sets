package pipeline

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"pr-review-automation/internal/config"
	"pr-review-automation/internal/domain"

	"github.com/stretchr/testify/assert"
)

func TestChunkReviewer_ReviewChunked(t *testing.T) {
	// Base configuration for testing
	baseCfg := config.DegradationConfig{
		L2ChunkByFile:     true,
		L2MaxFailureRatio: 0.5,
		L2FailFast:        false,
	}

	tests := []struct {
		name          string
		cfg           config.DegradationConfig
		setupMock     func(ctx context.Context, req ReviewRequest, changes []FileChange, contextFiles []FileContent) (*domain.ReviewResult, error)
		files         []FileChange // Simulate multiple files to force chunking
		expectError   bool
		expectedScore int
	}{
		{
			name: "All chunks success",
			cfg:  baseCfg,
			setupMock: func(ctx context.Context, req ReviewRequest, changes []FileChange, contextFiles []FileContent) (*domain.ReviewResult, error) {
				return &domain.ReviewResult{
					Comments: []domain.ReviewComment{{Comment: "LGTM"}},
					Score:    90,
					Summary:  "Good",
				}, nil
			},
			files:         makeFiles(10), // Sufficient to create multiple chunks
			expectError:   false,
			expectedScore: 90,
		},
		{
			name: "Partial failure within threshold",
			cfg:  baseCfg, // Ratio 0.5
			setupMock: func(ctx context.Context, req ReviewRequest, changes []FileChange, contextFiles []FileContent) (*domain.ReviewResult, error) {
				// Fail every 3rd chunk
				if len(changes) > 0 && changes[0].Path == "file_2" { // Mock failures based on content
					return nil, errors.New("mock error")
				}
				return &domain.ReviewResult{
					Comments: []domain.ReviewComment{{Comment: "LGTM"}},
					Score:    80,
					Summary:  "Good",
				}, nil
			},
			files: []FileChange{{Path: "file_1"}, {Path: "file_2"}, {Path: "file_3"}}, // Assuming 1 file per chunk logic or similar
			// Note: Chunking logic depends on token size. We need to ensure logic produces multiple chunks.
			// In ReviewChunked implementation, single file groups are checked against available tokens.
			// To simplify, we can assume 'files' generate chunks.
			// If each file is small, they might be grouped.
			// We need to manipulate tokens or files to force chunks.
			// Actually, let's just trust ReviewChunked splits if we give it enough "files" and set maxTokens low?
			// Or we can mock EstimateTokens?
			// The ChunkReviewer uses EstimateTokens global function.

			// Strategy: Set maxTokens very low to force 1 file per chunk.
			// base prompt ~0 tokens for test (empty string).
			// file diff ~10 tokens?
			expectError:   false,
			expectedScore: 80,
		},
		{
			name: "Failure exceeds threshold",
			cfg:  config.DegradationConfig{L2MaxFailureRatio: 0.2, L2FailFast: false},
			setupMock: func(ctx context.Context, req ReviewRequest, changes []FileChange, contextFiles []FileContent) (*domain.ReviewResult, error) {
				// Fail 2 out of 3
				if len(changes) > 0 && (changes[0].Path == "file_2" || changes[0].Path == "file_3") {
					return nil, errors.New("mock error")
				}
				return &domain.ReviewResult{Score: 80}, nil
			},
			files:       []FileChange{{Path: "file_1"}, {Path: "file_2"}, {Path: "file_3"}},
			expectError: true,
		},
		{
			name: "FailFast triggered",
			cfg:  config.DegradationConfig{L2MaxFailureRatio: 1.0, L2FailFast: true},
			setupMock: func(ctx context.Context, req ReviewRequest, changes []FileChange, contextFiles []FileContent) (*domain.ReviewResult, error) {
				if len(changes) > 0 && changes[0].Path == "file_1" {
					return nil, errors.New("mock error")
				}
				return &domain.ReviewResult{Score: 80}, nil
			},
			files:       []FileChange{{Path: "file_1"}, {Path: "file_2"}},
			expectError: true,
		},
		{
			name: "All chunks failed",
			cfg:  baseCfg,
			setupMock: func(ctx context.Context, req ReviewRequest, changes []FileChange, contextFiles []FileContent) (*domain.ReviewResult, error) {
				return nil, errors.New("mock error")
			},
			files:       []FileChange{{Path: "file_1"}},
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cr := NewChunkReviewer(100, tt.cfg) // Low max tokens to force chunking
			// To ensure chunking happens, we can assume 1 file per chunk if maxTokens is small and EstimateTokens returns non-zero.
			// EstimateTokens(text) = len/3.5
			// If we make file paths long enough or HunkLines long enough...

			// Let's populate files with content to ensure tokens > 0
			for i := range tt.files {
				tt.files[i].HunkLines = []string{fmt.Sprintf("some content line %d", i)} // ~20 chars = ~5 tokens
			}

			// With maxTokens=100, basePrompt="" -> available=90.
			// If each file is ~5 tokens, they might be grouped.
			// We need to force split.
			// If we want 1 chunk per file, we can make each file ~50 tokens.
			for i := range tt.files {
				tt.files[i].HunkLines = []string{string(make([]byte, 200))} // ~200 chars = ~57 tokens
			}
			// Now 2 files (57+57 > 90) should split.

			res, err := cr.ReviewChunked(context.Background(), ReviewRequest{}, tt.files, nil, "", tt.setupMock)

			if tt.expectError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				if res != nil {
					assert.Equal(t, tt.expectedScore, res.Score)
				}
			}
		})
	}
}

func makeFiles(n int) []FileChange {
	f := make([]FileChange, n)
	for i := 0; i < n; i++ {
		f[i] = FileChange{Path: fmt.Sprintf("file_%d", i)}
	}
	return f
}
