package context

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"
	"sync"

	sitter "github.com/smacker/go-tree-sitter"
	"github.com/smacker/go-tree-sitter/cpp"
	"github.com/smacker/go-tree-sitter/golang"
	"github.com/smacker/go-tree-sitter/java"
	"github.com/smacker/go-tree-sitter/python"
)

// ContextEngine handles semantic analysis of code files using Tree-sitter
type ContextEngine struct {
	// parserPool reuses sitter.Parser instances to reduce CGo and allocation overhead
	parserPool sync.Pool
	// grammars maps file extensions to Tree-sitter languages
	grammars map[string]*sitter.Language
}

// FileAnalysis contains the result of semantic analysis
type FileAnalysis struct {
	Path          string
	Language      string
	Dependencies  []Dependency
	Chunks        []Chunk
	SQLReferences []SQLReference
	// Future fields: Classes, etc.
}

// Dependency represents an imported or included file
type Dependency struct {
	Path   string // The raw path string extracted from source
	Type   string // e.g., "import", "include", "package"
	Line   int    // Line number (1-based)
	Source string // The actual code line
}

// Chunk represents a semantic unit of code (function, class, etc.)
type Chunk struct {
	Name      string // Identifier name
	Type      string // "function", "method"
	StartLine int    // 1-based
	EndLine   int    // 1-based
	Content   string
}

// SQLReference represents an embedded SQL query or a reference to a SQL file
type SQLReference struct {
	Type    string // "embedded", "file_ref"
	Content string // SQL string or filename
	Line    int    // 1-based
}

// NewContextEngine creates a new ContextEngine instance
func NewContextEngine() *ContextEngine {
	return &ContextEngine{
		parserPool: sync.Pool{
			New: func() interface{} {
				return sitter.NewParser()
			},
		},
		grammars: map[string]*sitter.Language{
			".go":   golang.GetLanguage(),
			".cpp":  cpp.GetLanguage(),
			".cc":   cpp.GetLanguage(),
			".cxx":  cpp.GetLanguage(),
			".c":    cpp.GetLanguage(),
			".h":    cpp.GetLanguage(),
			".hpp":  cpp.GetLanguage(),
			".hxx":  cpp.GetLanguage(),
			".py":   python.GetLanguage(),
			".java": java.GetLanguage(),
		},
	}
}

// Analyze parses the file and extracts semantic information
// changedLines can be used to optimize analysis (e.g., only re-parse relevant chunks),
// but for now we parse the whole file for extracting global dependencies.
func (e *ContextEngine) Analyze(ctx context.Context, path string, source []byte) (*FileAnalysis, error) {
	ext := strings.ToLower(filepath.Ext(path))
	lang, ok := e.grammars[ext]
	if !ok {
		return nil, fmt.Errorf("unsupported language for extension: %s", ext)
	}

	analysis := &FileAnalysis{
		Path:     path,
		Language: ext,
	}

	// Get parser from pool
	parser := e.parserPool.Get().(*sitter.Parser)
	defer e.parserPool.Put(parser)

	parser.SetLanguage(lang)

	// Parse
	tree, err := parser.ParseCtx(ctx, nil, source)
	if err != nil {
		return nil, fmt.Errorf("tree-sitter parse failed: %w", err)
	}
	defer tree.Close() // Tree must be closed to free C memory

	// Extract Dependencies
	analysis.Dependencies = e.extractDependencies(tree, source, ext)

	// Extract Semantic Chunks
	analysis.Chunks = e.extractChunks(tree, source, ext)

	// Extract SQL References
	analysis.SQLReferences = e.extractSQL(tree, source, ext)

	slog.Info("ContextEngine: Analysis complete",
		"file", path,
		"lang", ext,
		"chunks", len(analysis.Chunks),
		"deps", len(analysis.Dependencies),
		"sql_refs", len(analysis.SQLReferences))

	return analysis, nil
}
