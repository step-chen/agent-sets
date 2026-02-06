package processor

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"text/template"
)

// CommentTemplates holds all parsed comment templates
type CommentTemplates struct {
	Summary *template.Template
	Inline  *template.Template
	Addons  *template.Template
}

// TemplateLoader handles loading and caching of comment templates
type TemplateLoader struct {
	promptsDir string
	cache      *CommentTemplates
	mu         sync.RWMutex
}

// NewTemplateLoader creates a new TemplateLoader
func NewTemplateLoader(promptsDir string) *TemplateLoader {
	return &TemplateLoader{promptsDir: promptsDir}
}

// Load loads all comment templates from disk
func (l *TemplateLoader) Load() (*CommentTemplates, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	// If already loaded, return cache (could add forced reload flag if needed)
	if l.cache != nil {
		return l.cache, nil
	}

	templates := &CommentTemplates{}
	var err error

	// Helper to parse a template with components
	parse := func(name string) (*template.Template, error) {
		t := template.New(filepath.Base(name))
		// Join all file paths including components
		var allFiles []string
		// Add main template
		allFiles = append(allFiles, filepath.Join(l.promptsDir, "comments", name))
		// Add components
		componentDir := filepath.Join(l.promptsDir, "comments", "components")
		entries, err := os.ReadDir(componentDir)
		if err == nil {
			for _, entry := range entries {
				if !entry.IsDir() && filepath.Ext(entry.Name()) == ".tmpl" {
					allFiles = append(allFiles, filepath.Join(componentDir, entry.Name()))
				}
			}
		} else {
			// It's okay if components dir doesn't exist, though our design relies on it
			// But for robust error handling, maybe we should warn or just check if it's missing
			if !os.IsNotExist(err) {
				return nil, err
			}
		}

		return t.ParseFiles(allFiles...)
	}

	templates.Summary, err = parse("summary.tmpl")
	if err != nil {
		// Log or handle individual failure? For now strict failure.
		// Fallback logic could go here if we wanted to use embedded defaults.
		return nil, fmt.Errorf("failed to parse summary.tmpl: %w", err)
	}

	templates.Inline, err = parse("inline.tmpl")
	if err != nil {
		return nil, fmt.Errorf("failed to parse inline.tmpl: %w", err)
	}

	templates.Addons, err = parse("addons.tmpl")
	if err != nil {
		return nil, fmt.Errorf("failed to parse addons.tmpl: %w", err)
	}

	l.cache = templates
	return templates, nil
}

// Get returns cached templates, loading if necessary
func (l *TemplateLoader) Get() (*CommentTemplates, error) {
	l.mu.RLock()
	if l.cache != nil {
		defer l.mu.RUnlock()
		return l.cache, nil
	}
	l.mu.RUnlock()
	return l.Load()
}

// --- Data Structures ---

// MarkerData for marker.tmpl
type MarkerData struct {
	Type   string // "summary", "file", or empty for inline
	File   string
	Line   int
	Commit string
}

// FooterData for footer.tmpl
type FooterData struct {
	Model          string
	Confidence     float64
	ShowConfidence bool
}

// TableData for table.tmpl
type TableData struct {
	Headers []string
	Rows    [][]string // Pre-escaped, pre-formatted
}

// SummaryTemplateData for summary.tmpl
type SummaryTemplateData struct {
	MarkerData
	FooterData
	Score   int
	Content string
	Addons  string // Pre-rendered addons table (HTML-safe)
}

// InlineTemplateData for inline.tmpl (covers both individual and file-merged)
type InlineTemplateData struct {
	MarkerData
	FooterData
	Icon      string     // Optional: ⚠️ or 🚫
	Title     string     // Optional: "filename Code Review"
	Content   string     // For single comment
	TableData *TableData // For merged comments
}

// AddonsTemplateData for addons.tmpl
type AddonsTemplateData struct {
	TableData
	ShowConfidence bool
}
