package pipeline

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestExtractFilePath_StateMachine(t *testing.T) {
	p := NewDiffPreprocessor(PreprocessOptions{})

	tests := []struct {
		name     string
		diff     string
		expected string
	}{
		{
			name:     "Standard Git",
			diff:     "diff --git a/file.go b/file.go\nindex abc..def\n--- a/file.go\n+++ b/file.go\n@@ ...",
			expected: "file.go",
		},
		{
			name:     "Bitbucket SVN",
			diff:     "diff --git src://trunk/file.cpp dst://trunk/file.cpp\n+++ dst://trunk/file.cpp\n@@ ...",
			expected: "trunk/file.cpp",
		},
		{
			name:     "Rename",
			diff:     "diff --git a/old.go b/new.go\nsimilarity index 95%\nrename from old.go\nrename to new.go\n+++ b/new.go",
			expected: "new.go",
		},
		{
			name:     "Rename No Content Change",
			diff:     "diff --git a/old.go b/new.go\nsimilarity index 100%\nrename from old.go\nrename to new.go",
			expected: "new.go",
		},
		{
			name:     "Copy",
			diff:     "diff --git a/src.go b/dst.go\ncopy from src.go\ncopy to dst.go\n+++ b/dst.go",
			expected: "dst.go",
		},
		{
			name:     "New File",
			diff:     "diff --git a/new.go b/new.go\nnew file mode 100644\nindex 000..abc\n--- /dev/null\n+++ b/new.go",
			expected: "new.go",
		},
		{
			name:     "Deleted File (Use Fallback)",
			diff:     "diff --git a/del.go b/del.go\ndeleted file mode 100644\nindex abc..000\n--- a/del.go\n+++ /dev/null",
			expected: "del.go",
		},
		{
			name:     "Quoted Path Standard",
			diff:     "diff --git \"a/my file.go\" \"b/my file.go\"\n+++ \"b/my file.go\"",
			expected: "my file.go",
		},
		{
			name:     "Quoted Path Fallback",
			diff:     "diff --git \"a/my file.go\" \"b/my file.go\"\ndeleted file mode 100644\n--- \"a/my file.go\"\n+++ /dev/null",
			expected: "my file.go",
		},
		{
			name:     "Truncated Diff (Bug Fix)",
			diff:     "diff --git a/file.go b/file.go\nindex abc... [TRUNCATED]",
			expected: "file.go",
		},
		{
			name:     "Bitbucket Header with Newline Bug",
			diff:     "diff --git src://trunk/src/Common/ConvertGDBConstants.h dst://trunk/src/Common/ConvertGDBConstants.h\nindex\n+++ dst://trunk/src/Common/ConvertGDBConstants.h",
			expected: "trunk/src/Common/ConvertGDBConstants.h",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := p.ExtractFilePath(tt.diff)
			assert.Equal(t, tt.expected, got)
		})
	}
}

func TestExtractDstPath(t *testing.T) {
	tests := []struct {
		line     string
		expected string
	}{
		{"diff --git a/foo b/bar", "bar"},
		{"diff --git a/foo b/foo", "foo"},
		{"diff --git \"a/foo bar\" \"b/foo bar\"", "foo bar"},
		{"diff --git src://foo dst://bar", "bar"},
	}

	for _, tt := range tests {
		t.Run(tt.line, func(t *testing.T) {
			got := extractDstPath(tt.line)
			assert.Equal(t, tt.expected, got)
		})
	}
}

func TestParseFiles_Integration(t *testing.T) {
	// Use DiffSplitter which internally uses DiffPreprocessor
	s := NewDiffSplitter(0, 0)

	tests := []struct {
		name     string
		fullDiff string
		expected []string // Expected paths
	}{
		{
			name:     "Standard Multi-File",
			fullDiff: "diff --git a/file1.go b/file1.go\nindex 111..222\n+++ b/file1.go\n@@ -1 +1 @@\n-old\n+new\ndiff --git a/file2.go b/file2.go\nindex 333..444\n+++ b/file2.go\n@@ -1 +1 @@\n-old\n+new",
			expected: []string{"file1.go", "file2.go"},
		},
		{
			name:     "Bug Fix: Newline in Index",
			fullDiff: "diff --git a/bug.go b/bug.go\nindex\n+++ b/bug.go\n@@ -1 +1 @@\n-old\n+new",
			expected: []string{"bug.go"},
		},
		{
			name:     "Mixed Formats",
			fullDiff: "diff --git a/std.go b/std.go\n+++ b/std.go\ndiff --git src://old dst://new\n+++ dst://new",
			expected: []string{"std.go", "new"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			files := s.ParseFiles(tt.fullDiff)
			var paths []string
			for _, f := range files {
				paths = append(paths, f.Path)
			}
			assert.Equal(t, tt.expected, paths)
		})
	}
}
