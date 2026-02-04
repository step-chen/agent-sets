package context

import (
	"log/slog"
	"strings"

	sitter "github.com/tree-sitter/go-tree-sitter"
)

// extractDependencies implementation
func (e *ContextEngine) extractDependencies(tree *sitter.Tree, source []byte, ext string) []Dependency {
	var queryStr string
	switch ext {
	case ".go":
		queryStr = goDependencyQuery
	case ".cpp", ".cc", ".cxx", ".c", ".h", ".hpp", ".hxx":
		queryStr = cppDependencyQuery
	case ".py":
		queryStr = pythonDependencyQuery
	case ".java":
		queryStr = javaDependencyQuery
	default:
		return []Dependency{}
	}

	lang, ok := e.grammars[ext]
	if !ok {
		return []Dependency{}
	}

	q, err := sitter.NewQuery(lang, queryStr)
	if err != nil {
		slog.Error("Failed to create tree-sitter query", "lang", ext, "error", err)
		return []Dependency{}
	}
	defer q.Close()

	cursor := sitter.NewQueryCursor()
	defer cursor.Close()

	iter := cursor.Matches(q, tree.RootNode(), source)

	var deps []Dependency
	seen := make(map[string]bool)

	for match := iter.Next(); match != nil; match = iter.Next() {
		for _, capture := range match.Captures {
			// Check capture name is "path"
			name := q.CaptureNames()[capture.Index]
			if name != "path" {
				continue
			}

			node := capture.Node
			rawText := node.Utf8Text(source)
			cleanPath := cleanPathString(rawText)

			if seen[cleanPath] {
				continue
			}
			seen[cleanPath] = true

			deps = append(deps, Dependency{
				Path:   cleanPath,
				Type:   determineImportType(ext, rawText),
				Line:   int(node.StartPosition().Row) + 1,
				Source: rawText,
			})
		}
	}
	return deps
}

func cleanPathString(raw string) string {
	return strings.Trim(raw, "\"`<>")
}

func determineImportType(ext, raw string) string {
	if ext == ".go" || ext == ".py" || ext == ".java" {
		return "import"
	}
	// C++
	if strings.HasPrefix(raw, "<") {
		return "system_include"
	}
	return "include"
}
