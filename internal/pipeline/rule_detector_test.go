package pipeline

import (
	"reflect"
	"sort"
	"testing"
)

func TestRuleDetector_Detect(t *testing.T) {
	tests := []struct {
		name     string
		changes  []FileChange
		expected []string
	}{
		{
			name: "Go file detected",
			changes: []FileChange{
				{Path: "main.go"},
			},
			expected: []string{"go"},
		},
		{
			name: "C++ files detected",
			changes: []FileChange{
				{Path: "main.cpp"},
				{Path: "header.hpp"},
			},
			expected: []string{"cpp"},
		},
		{
			name: "Mixed Go and C++",
			changes: []FileChange{
				{Path: "main.go"},
				{Path: "utils.cpp"},
			},
			expected: []string{"cpp", "go"},
		},
		{
			name: "Dockerfile filename match",
			changes: []FileChange{
				{Path: "Dockerfile"},
				{Path: "Dockerfile.dev"},
			},
			expected: []string{"docker"},
		},
		{
			name: "Python stubs and scripts",
			changes: []FileChange{
				{Path: "script.py"},
				{Path: "types.pyi"},
			},
			expected: []string{"py"},
		},
		{
			name: "Embedded SQL in Go",
			changes: []FileChange{
				{
					Path: "db.go",
					HunkLines: []string{
						" func GetUser() {",
						"+    query := \"SELECT * FROM users\"",
						" }",
					},
				},
			},
			expected: []string{"go", "sql"},
		},
		{
			name: "No embedded SQL in deleted lines",
			changes: []FileChange{
				{
					Path: "db.go",
					HunkLines: []string{
						"-    query := \"SELECT * FROM users\"",
						"+    query := \"something else\"",
					},
				},
			},
			expected: []string{"go"},
		},
		{
			name: "SQL file extension",
			changes: []FileChange{
				{Path: "schema.sql"},
			},
			expected: []string{"sql"},
		},
		{
			name: "Case insensitive extensions",
			changes: []FileChange{
				{Path: "MAIN.CPP"},
				{Path: "Script.Py"},
			},
			expected: []string{"cpp", "py"},
		},
		{
			name: "Java file detected",
			changes: []FileChange{
				{Path: "Service.java"},
			},
			expected: []string{"java"},
		},
		{
			name: "K8s manifest detected by content",
			changes: []FileChange{
				{
					Path: "deployment.yaml",
					HunkLines: []string{
						"+apiVersion: apps/v1",
						"+kind: Deployment",
						"+metadata:",
						"+  name: nginx",
					},
				},
			},
			expected: []string{"k8s"},
		},
		{
			name: "Generic YAML not detected as K8s",
			changes: []FileChange{
				{
					Path: "config.yaml",
					HunkLines: []string{
						"+server:",
						"+  port: 8080",
					},
				},
			},
			expected: []string{}, // No k8s
		},
	}

	detector := NewRuleDetector()

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := detector.Detect(tt.changes)
			// Sort for deterministic comparison
			sort.Strings(got)
			sort.Strings(tt.expected)

			if !reflect.DeepEqual(got, tt.expected) {
				t.Errorf("Detect() = %v, want %v", got, tt.expected)
			}
		})
	}
}
