package processor

import (
	"testing"

	"pr-review-automation/internal/config"
	"pr-review-automation/internal/domain"

	"github.com/stretchr/testify/assert"
)

func TestFilterLowConfidence(t *testing.T) {
	tests := []struct {
		name          string
		minConfidence float64
		input         []domain.ReviewComment
		expected      []domain.ReviewComment
	}{
		{
			name:          "Filters below threshold",
			minConfidence: 0.7,
			input: []domain.ReviewComment{
				{Comment: "High confidence", Confidence: 0.8},
				{Comment: "Low confidence", Confidence: 0.6},
				{Comment: "Exact threshold", Confidence: 0.7},
			},
			expected: []domain.ReviewComment{
				{Comment: "High confidence", Confidence: 0.8},
				{Comment: "Exact threshold", Confidence: 0.7},
			},
		},
		{
			name:          "Disabled filter (threshold 0)",
			minConfidence: 0.0,
			input: []domain.ReviewComment{
				{Comment: "Should keep", Confidence: 0.1},
			},
			expected: []domain.ReviewComment{
				{Comment: "Should keep", Confidence: 0.1},
			},
		},
		{
			name:          "Empty input",
			minConfidence: 0.7,
			input:         []domain.ReviewComment{},
			expected:      nil, // or empty slice, depending on impl
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &config.Config{
				Pipeline: config.PipelineConfig{
					AntiHallucination: config.AntiHallucinationConfig{
						MinConfidence: tt.minConfidence,
					},
				},
			}
			p := &PRProcessor{cfg: cfg}

			got := p.filterLowConfidence(tt.input)
			assert.Equal(t, tt.expected, got)
		})
	}
}
