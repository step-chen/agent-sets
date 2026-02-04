package pipeline

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func Test_deduplicateDiffs(t *testing.T) {
	var preprocessor *DiffPreprocessor = NewDiffPreprocessor(PreprocessOptions{})

	tests := []struct {
		name         string
		fileDiffStrs []string
		expected     []string // Expected paths
	}{
		{
			name: "No duplicates",
			fileDiffStrs: []string{
				"diff --git a/file1.go b/file1.go\nindex 111..222\n+++ b/file1.go\n@@ -1 +1 @@\n-old\n+new",
				"diff --git a/file2.go b/file2.go\nindex 333..444\n+++ b/file2.go\n@@ -1 +1 @@\n-old2\n+new2",
			},
			expected: []string{"file1.go", "file2.go"},
		},
		{
			name: "Duplicate content (same change)",
			fileDiffStrs: []string{
				"diff --git a/file1.go b/file1.go\nindex 111..222\n+++ b/file1.go\n@@ -1 +1 @@\n-old\n+new",
				"diff --git a/file1_copy.go b/file1_copy.go\nindex 555..666\n+++ b/file1_copy.go\n@@ -1 +1 @@\n-old\n+new",
			},
			expected: []string{"file1.go"}, // Only first one kept
		},
		{
			name: "Different content",
			fileDiffStrs: []string{
				"diff --git a/file1.go b/file1.go\nindex 111..222\n+++ b/file1.go\n@@ -1 +1 @@\n-old\n+new",
				"diff --git a/file2.go b/file2.go\nindex 333..444\n+++ b/file2.go\n@@ -1 +1 @@\n-old\n+different",
			},
			expected: []string{"file1.go", "file2.go"},
		},
		{
			name: "Ignore metadata differences",
			fileDiffStrs: []string{
				"diff --git a/file1.go b/file1.go\nindex 111..222\n+++ b/file1.go\n@@ -1 +1 @@\n-content\n+changed",
				"diff --git a/folder/file1.go b/folder/file1.go\nindex 999..000\n+++ b/folder/file1.go\n@@ -10 +10 @@\n-content\n+changed",
			},
			expected: []string{"file1.go"}, // Should be different header/index
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			changes := deduplicateDiffs(tt.fileDiffStrs, preprocessor, nil)
			var paths []string
			for _, c := range changes {
				paths = append(paths, c.Path)
			}
			assert.Equal(t, tt.expected, paths)
		})
	}
}

func Test_computeDiffHash(t *testing.T) {
	content1 := []string{
		"diff --git a/foo b/foo",
		"index abc..def",
		"--- a/foo",
		"+++ b/foo",
		"@@ -1,2 +1,2 @@",
		" item1",
		"+item2",
	}
	content2 := []string{
		"diff --git a/bar b/bar", // Different header
		"index 123..456",         // Different index
		"--- a/bar",
		"+++ b/bar",
		"@@ -5,6 +5,6 @@", // Different line numbers
		" item1",
		"+item2", // Same content
	}

	hash1 := computeDiffHash(content1)
	hash2 := computeDiffHash(content2)

	assert.Equal(t, hash1, hash2, "Hashes should be equal for identical content regardless of headers")
}
