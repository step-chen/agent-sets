package context

import (
	"log/slog"

	sitter "github.com/tree-sitter/go-tree-sitter"
)

// extractChunks extracts semantic chunks (functions, methods) from the source code
func (e *ContextEngine) extractChunks(tree *sitter.Tree, source []byte, ext string) []Chunk {
	var queryStr string
	switch ext {
	case ".go":
		queryStr = goChunkQuery
	case ".cpp", ".cc", ".cxx", ".c", ".h", ".hpp", ".hxx":
		queryStr = cppChunkQuery
	case ".py":
		queryStr = pythonChunkQuery
	case ".java":
		queryStr = javaChunkQuery
	default:
		return []Chunk{}
	}

	lang, ok := e.grammars[ext]
	if !ok {
		return []Chunk{}
	}

	q, err := sitter.NewQuery(lang, queryStr)
	if err != nil {
		slog.Error("Failed to create tree-sitter query for chunking", "lang", ext, "error", err)
		return []Chunk{}
	}
	defer q.Close()

	cursor := sitter.NewQueryCursor()
	defer cursor.Close()

	iter := cursor.Matches(q, tree.RootNode(), source)
	var chunks []Chunk

	for match := iter.Next(); match != nil; match = iter.Next() {
		// Since we have multiple captures (@name, @body), we need to handle them within the match
		// The query is structured so that one match corresponds to one function/method
		// But in tree-sitter-go matches, captures are grouped.

		// Initialize with invalid values to detect if we found both parts
		// Actually, we can just iterate captures.
		// Captures in a match are related.

		var bodyNode, nameNode *sitter.Node
		var chunkType string

		// Iterate captures in the match
		for _, capture := range match.Captures {
			name := q.CaptureNames()[capture.Index]

			// Go style: capture entire node
			if name == "function" {
				bodyNode = &capture.Node
				chunkType = "function"
				nameNode = bodyNode.ChildByFieldName("name")
			} else if name == "method" {
				bodyNode = &capture.Node
				chunkType = "method"
			} else if name == "class" {
				bodyNode = &capture.Node
				chunkType = "class"
				nameNode = bodyNode.ChildByFieldName("name")
			} else if name == "body" {
				// C++ style: body captured separately
				bodyNode = &capture.Node
				chunkType = "function"
			} else if name == "name" {
				// C++ style: name captured separately
				nameNode = &capture.Node
			} else if name == "func_name.name" { // Python/Java
				nameNode = &capture.Node
			} else if name == "func.body" {
				// Specific for cases where we capture body separately
				bodyNode = &capture.Node
				chunkType = "function"
			}
		}

		if bodyNode != nil {
			var chunk Chunk
			chunk.Content = bodyNode.Utf8Text(source)
			chunk.StartLine = int(bodyNode.StartPosition().Row) + 1
			chunk.EndLine = int(bodyNode.EndPosition().Row) + 1
			chunk.Type = chunkType
			if chunk.Type == "" {
				chunk.Type = "function"
			}

			if nameNode != nil {
				chunk.Name = nameNode.Utf8Text(source)
			} else {
				chunk.Name = "anonymous"
			}
			chunks = append(chunks, chunk)
		}
	}
	return chunks
}
