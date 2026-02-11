package pipeline

import (
	"testing"
)

func TestExtractJSON(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "Pure JSON",
			input:    `{"foo":"bar"}`,
			expected: `{"foo":"bar"}`,
		},
		{
			name:     "Markdown JSON",
			input:    "```json\n{\"foo\":\"bar\"}\n```",
			expected: `{"foo":"bar"}`,
		},
		{
			name:     "Markdown No Lang",
			input:    "```\n{\"foo\":\"bar\"}\n```",
			expected: `{"foo":"bar"}`,
		},
		{
			name:     "Think Tag Prefix",
			input:    `<think>Some reasoning here...</think> {"foo":"bar"}`,
			expected: `{"foo":"bar"}`,
		},
		{
			name:     "Think Tag Multiline",
			input:    "<think>\nReasoning\nKey\n</think>\n{\"foo\":\"bar\"}",
			expected: `{"foo":"bar"}`,
		},
		{
			name:     "Extra Text Suffix", // The case reported: invalid char )
			input:    `{"foo":"bar"} )`,
			expected: `{"foo":"bar"}`,
		},
		{
			name:     "Extra Text Prefix",
			input:    `Here is the result: {"foo":"bar"}`,
			expected: `{"foo":"bar"}`,
		},
		{
			name:     "Complex Mixed",
			input:    "<think>Reasoning</think>\nHere is JSON:\n```json\n{\"foo\":\"bar\"}\n```",
			expected: `{"foo":"bar"}`,
		},
		{
			name:     "Nested Braces",
			input:    `pre {"foo":{"bar":1}} post`,
			expected: `{"foo":{"bar":1}}`,
		},
		{
			name:     "No Braces",
			input:    `just text`,
			expected: `just text`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ExtractJSON(tt.input)
			if got != tt.expected {
				t.Errorf("ExtractJSON(%q) = %q, want %q", tt.input, got, tt.expected)
			}
		})
	}
}
