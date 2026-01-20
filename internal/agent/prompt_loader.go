package agent

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// PromptLoader loads prompts from filesystem with fallback hierarchy
type PromptLoader struct {
	baseDir string
}

// NewPromptLoader creates a new prompt loader with the given base directory
func NewPromptLoader(baseDir string) *PromptLoader {
	return &PromptLoader{baseDir: baseDir}
}

// Load returns prompt content with fallback hierarchy:
// 1. {baseDir}/{project}/{language}.md
// 2. {baseDir}/{project}/default.md
// 3. {baseDir}/default/{language}.md
// 4. {baseDir}/default/default.md
func (l *PromptLoader) Load(project, language string) (string, error) {
	candidates := []string{
		filepath.Join(l.baseDir, project, language+".md"),
		filepath.Join(l.baseDir, project, "default.md"),
		filepath.Join(l.baseDir, "default", language+".md"),
		filepath.Join(l.baseDir, "default", "default.md"),
	}

	for _, path := range candidates {
		data, err := os.ReadFile(path)
		if err == nil {
			return string(data), nil
		}
		if !os.IsNotExist(err) {
			return "", fmt.Errorf("read prompt %s: %w", path, err)
		}
	}

	return "", fmt.Errorf("no prompt found for project=%q language=%q, tried: %v", project, language, candidates)
}

// LoadPrompt loads a specific prompt file directly from the base directory.
// Name should be relative path without extension, e.g. "system/webhook_parser"
func (l *PromptLoader) LoadPrompt(name string) (string, error) {
	path := filepath.Join(l.baseDir, name+".md")
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read prompt %s: %w", path, err)
	}
	return string(data), nil
}

// languageExtensions maps file extensions to language identifiers
var languageExtensions = map[string]string{
	".cpp": "cpp", ".cc": "cpp", ".cxx": "cpp", ".c": "cpp", ".h": "cpp", ".hpp": "cpp", ".hxx": "cpp",
	".go":   "golang",
	".py":   "python",
	".java": "java",
	".ts":   "typescript", ".tsx": "typescript",
	".js": "javascript", ".jsx": "javascript",
	".rs": "rust",
	".kt": "kotlin", ".kts": "kotlin",
	".swift": "swift",
	".rb":    "ruby",
	".cs":    "csharp",
}

// DetectLanguage detects the primary language from a list of file paths
// based on file extensions. Returns "default" if no language is detected.
func DetectLanguage(files []string) string {
	counts := make(map[string]int)

	for _, file := range files {
		ext := strings.ToLower(filepath.Ext(file))
		if lang, ok := languageExtensions[ext]; ok {
			counts[lang]++
		}
	}

	if len(counts) == 0 {
		return "default"
	}

	// Find the language with the most files
	maxLang := "default"
	maxCount := 0
	for lang, count := range counts {
		if count > maxCount {
			maxCount = count
			maxLang = lang
		}
	}

	return maxLang
}
