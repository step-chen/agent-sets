package agent

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPromptLoader_Load(t *testing.T) {
	// Create temp directory structure
	tempDir := t.TempDir()

	// Setup: prompts/default/default.md
	defaultDir := filepath.Join(tempDir, "default")
	if err := os.MkdirAll(defaultDir, 0755); err != nil {
		t.Fatalf("create default dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(defaultDir, "default.md"), []byte("default prompt"), 0644); err != nil {
		t.Fatalf("write default.md: %v", err)
	}
	if err := os.WriteFile(filepath.Join(defaultDir, "golang.md"), []byte("golang prompt"), 0644); err != nil {
		t.Fatalf("write golang.md: %v", err)
	}

	// Setup: prompts/myproject/cpp.md
	projectDir := filepath.Join(tempDir, "myproject")
	if err := os.MkdirAll(projectDir, 0755); err != nil {
		t.Fatalf("create project dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(projectDir, "cpp.md"), []byte("myproject cpp prompt"), 0644); err != nil {
		t.Fatalf("write cpp.md: %v", err)
	}

	loader := NewPromptLoader(tempDir)

	tests := []struct {
		name     string
		project  string
		language string
		want     string
		wantErr  bool
	}{
		{
			name:     "project specific language",
			project:  "myproject",
			language: "cpp",
			want:     "myproject cpp prompt",
		},
		{
			name:     "fallback to default language",
			project:  "myproject",
			language: "golang",
			want:     "golang prompt",
		},
		{
			name:     "fallback to default default",
			project:  "unknown",
			language: "unknown",
			want:     "default prompt",
		},
		{
			name:     "default project golang",
			project:  "default",
			language: "golang",
			want:     "golang prompt",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := loader.Load(tt.project, tt.language, nil)
			if (err != nil) != tt.wantErr {
				t.Errorf("Load() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("Load() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestPromptLoader_LoadError(t *testing.T) {
	tempDir := t.TempDir()
	loader := NewPromptLoader(tempDir)

	_, err := loader.Load("nonexistent", "nonexistent", nil)
	if err == nil {
		t.Error("expected error for missing prompt, got nil")
	}
}

func TestDetectLanguage(t *testing.T) {
	tests := []struct {
		name  string
		files []string
		want  string
	}{
		{
			name:  "cpp files",
			files: []string{"main.cpp", "util.hpp", "test.cc"},
			want:  "cpp",
		},
		{
			name:  "golang files",
			files: []string{"main.go", "util.go", "handler.go"},
			want:  "golang",
		},
		{
			name:  "python files",
			files: []string{"app.py", "utils.py"},
			want:  "python",
		},
		{
			name:  "mixed - majority wins",
			files: []string{"main.go", "handler.go", "script.py"},
			want:  "golang",
		},
		{
			name:  "empty",
			files: []string{},
			want:  "default",
		},
		{
			name:  "unknown extensions",
			files: []string{"readme.md", "config.yaml"},
			want:  "default",
		},
		{
			name:  "typescript files",
			files: []string{"app.ts", "component.tsx"},
			want:  "typescript",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := DetectLanguage(tt.files)
			if got != tt.want {
				t.Errorf("DetectLanguage() = %q, want %q", got, tt.want)
			}
		})
	}
}
