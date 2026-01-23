package aggregator

import (
	"testing"
)

func TestResultAggregator_DeduplicateComments(t *testing.T) {
	agg := NewResultAggregator()

	tests := []struct {
		name     string
		comments []ReviewComment
		want     int // Expected number of unique comments
	}{
		{
			name: "No duplicates",
			comments: []ReviewComment{
				{File: "main.go", Line: 10, Comment: "Fix this"},
				{File: "main.go", Line: 20, Comment: "Fix that"},
			},
			want: 2,
		},
		{
			name: "Exact duplicates (same file, line, comment)",
			comments: []ReviewComment{
				{File: "main.go", Line: 10, Comment: "Fix this"},
				{File: "main.go", Line: 10, Comment: "Fix this"},
			},
			want: 1,
		},
		{
			name: "Same file/line but different comment (should keep both)",
			comments: []ReviewComment{
				{File: "main.go", Line: 10, Comment: "Security issue"},
				{File: "main.go", Line: 10, Comment: "Performance issue"},
			},
			want: 2,
		},
		{
			name: "Different file same content (should keep both)",
			comments: []ReviewComment{
				{File: "a.go", Line: 10, Comment: "Fix this"},
				{File: "b.go", Line: 10, Comment: "Fix this"},
			},
			want: 2,
		},
		{
			name: "Long comment truncation check",
			comments: []ReviewComment{
				{File: "main.go", Line: 10, Comment: "This is a very long comment that exceeds the fifty character limit for fingerprinting and should be truncated..."},
				{File: "main.go", Line: 10, Comment: "This is a very long comment that exceeds the fifty character limit for fingerprinting and should be truncated... duplication"},
			},
			want: 1, // Only first 50 chars match
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := agg.deduplicateComments(tt.comments)
			if len(got) != tt.want {
				t.Errorf("deduplicateComments() length = %v, want %v", len(got), tt.want)
			}
		})
	}
}
