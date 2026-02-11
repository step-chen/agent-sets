package pipeline

import (
	"testing"
)

func TestRepairJSON(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "Missing comma between fields",
			input:    `{"a": 1 "b": 2}`,
			expected: `{"a": 1,"b": 2}`,
		},
		{
			name:     "Missing comma between string fields",
			input:    `{"a": "val" "b": "val"}`,
			expected: `{"a": "val","b": "val"}`,
		},
		{
			name:     "Missing comma in array",
			input:    `[{"a":1} {"b":2}]`,
			expected: `[{"a":1},{"b":2}]`,
		},
		{
			name:     "Missing comma mixed types",
			input:    `{"a": true "b": null "c": 123 "d": "str"}`,
			expected: `{"a": true,"b": null,"c": 123,"d": "str"}`,
		},
		{
			name:     "Already valid JSON",
			input:    `{"a": 1, "b": 2}`,
			expected: `{"a": 1, "b": 2}`,
		},
		{
			name:     "Nested structure",
			input:    `{"outer": {"inner": 1 "next": 2} "more": 3}`,
			expected: `{"outer": {"inner": 1,"next": 2},"more": 3}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := RepairJSON(tt.input)
			if got != tt.expected {
				t.Errorf("RepairJSON() = %q, want %q", got, tt.expected)
			}
		})
	}
}
