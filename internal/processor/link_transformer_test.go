package processor

import (
	"testing"
)

func TestTransformLinks(t *testing.T) {
	baseURL := "https://bitbucket.example.com/projects/PROJ/repos/repo/pull-requests/123"

	tests := []struct {
		name     string
		input    string
		prURL    string
		expected string
	}{
		{
			name:     "Empty PR URL",
			input:    "[file.go:10](file.go#L10)",
			prURL:    "",
			expected: "[file.go:10](file.go#L10)",
		},
		{
			name:     "Line Link Transformation",
			input:    "Check this: [src/main.go:42](src/main.go#L42)",
			prURL:    baseURL,
			expected: "Check this: [src/main.go:42](" + baseURL + "/diff#src/main.go?t=42)",
		},
		{
			name:     "File Link Transformation",
			input:    "Review [README.md](README.md)",
			prURL:    baseURL,
			expected: "Review [README.md](" + baseURL + "/diff#README.md)",
		},
		{
			name:     "Multiple Links",
			input:    "- [a.go:1](a.go#L1)\n- [b.go](b.go)",
			prURL:    baseURL,
			expected: "- [a.go:1](" + baseURL + "/diff#a.go?t=1)\n- [b.go](" + baseURL + "/diff#b.go)",
		},
		{
			name:     "Ignore External Links",
			input:    "[Google](https://google.com)",
			prURL:    baseURL,
			expected: "[Google](https://google.com)",
		},
		{
			name:     "Ignore Anchors",
			input:    "[Section](#section)",
			prURL:    baseURL,
			expected: "[Section](#section)",
		},
		{
			name:     "Mixed Content",
			input:    "Fix [bug](src/bug.py#L5) and see [docs](https://docs.com).",
			prURL:    baseURL,
			expected: "Fix [bug](" + baseURL + "/diff#src/bug.py?t=5) and see [docs](https://docs.com).",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := TransformLinks(tt.input, tt.prURL)
			if got != tt.expected {
				t.Errorf("TransformLinks() = %q, want %q", got, tt.expected)
			}
		})
	}
}
