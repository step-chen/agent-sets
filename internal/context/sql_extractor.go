package context

import (
	"log/slog"
	"strings"

	sitter "github.com/tree-sitter/go-tree-sitter"
)

// extractSQL extracts embedded SQL logic and file references
func (e *ContextEngine) extractSQL(tree *sitter.Tree, source []byte, ext string) []SQLReference {
	var queryStr string
	switch ext {
	case ".go":
		queryStr = goCallQuery
	case ".cpp", ".cc", ".cxx", ".c", ".h", ".hpp", ".hxx":
		queryStr = cppCallQuery
	case ".py":
		queryStr = pythonCallQuery
	case ".java":
		queryStr = javaCallQuery
	default:
		return []SQLReference{}
	}

	lang, ok := e.grammars[ext]
	if !ok {
		return []SQLReference{}
	}

	q, err := sitter.NewQuery(lang, queryStr)
	if err != nil {
		slog.Error("Failed to create tree-sitter query for SQL extraction", "lang", ext, "error", err)
		return []SQLReference{}
	}
	defer q.Close()

	cursor := sitter.NewQueryCursor()
	defer cursor.Close()

	iter := cursor.Matches(q, tree.RootNode(), source)

	var refs []SQLReference

	for match := iter.Next(); match != nil; match = iter.Next() {

		for _, capture := range match.Captures {
			node := capture.Node
			// We captured a call_expression

			// Extract function Name
			funcName := extractFunctionName(&node, source, ext)
			if funcName == "" {
				continue
			}

			// Check heuristic: Function name relevant?
			if !isDBRelatedInfo(funcName) {
				continue
			}

			// Check arguments
			// iterate children to find strings
			// This is a simplified traversal. For AST accuracy we should traverse `arguments` field.
			// But creating a new cursor for each node is expensive?
			// We can just walk the node's children manually since it's shallow.

			argsNode := node.ChildByFieldName("arguments")
			if argsNode == nil {
				continue
			}

			childCount := argsNode.ChildCount()
			for i := uint(0); i < childCount; i++ {
				arg := argsNode.Child(i)
				content := arg.Utf8Text(source)

				// Clean string
				if strings.HasPrefix(content, "\"") || strings.HasPrefix(content, "`") {
					clean := strings.Trim(content, "\"`")
					cleanUpper := strings.ToUpper(strings.TrimSpace(clean))

					// Case 1: Embedded SQL
					if isSQLStatement(cleanUpper) {
						refs = append(refs, SQLReference{
							Type:    "embedded",
							Content: clean,
							Line:    int(arg.StartPosition().Row) + 1,
						})
					}

					// Case 2: File Reference
					if strings.HasSuffix(clean, ".sql") {
						refs = append(refs, SQLReference{
							Type:    "file_ref",
							Content: clean,
							Line:    int(arg.StartPosition().Row) + 1,
						})
					}
				}
			}
		}
	}
	return refs
}

func extractFunctionName(callNode *sitter.Node, source []byte, ext string) string {
	if ext == ".java" {
		// Java: method_invocation -> name
		nameNode := callNode.ChildByFieldName("name")
		if nameNode != nil {
			return nameNode.Utf8Text(source)
		}
		return ""
	}

	// Go/C++/Python: function field is the callable
	funcNode := callNode.ChildByFieldName("function")
	if funcNode == nil {
		return ""
	}

	// If it's a selector expression (Go), we want the method name (right side)
	if ext == ".go" && funcNode.Kind() == "selector_expression" {
		field := funcNode.ChildByFieldName("field")
		if field != nil {
			return field.Utf8Text(source)
		}
	} else if ext == ".cpp" && funcNode.Kind() == "field_expression" {
		// C++: obj.method() -> field_expression
		field := funcNode.ChildByFieldName("field")
		if field != nil {
			return field.Utf8Text(source)
		}
	} else if ext == ".py" && funcNode.Kind() == "attribute" {
		// Python: obj.method() -> attribute
		// function field of call is the attribute node itself
		attr := funcNode.ChildByFieldName("attribute")
		if attr != nil {
			return attr.Utf8Text(source)
		}
	}

	// Fallback to full content (simple calls)
	return funcNode.Utf8Text(source)
}

func isDBRelatedInfo(name string) bool {
	lower := strings.ToLower(name)
	keywords := []string{"query", "exec", "select", "get", "prepare", "fetch", "load"}
	for _, kw := range keywords {
		if strings.Contains(lower, kw) {
			return true
		}
	}
	return false
}

func isSQLStatement(s string) bool {
	prefixes := []string{"SELECT", "INSERT", "UPDATE", "DELETE", "CREATE", "DROP", "ALTER", "WITH"}
	for _, p := range prefixes {
		if strings.HasPrefix(s, p) {
			return true
		}
	}
	return false
}
