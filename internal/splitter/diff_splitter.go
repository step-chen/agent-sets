package splitter

import (
	"log/slog"
	"regexp"
	"strings"
)

// FileDiff represents a diff for a single file
type FileDiff struct {
	Path    string
	Content string
	Tokens  int // Estimated token count
}

// DiffChunk represents a group of file diffs that fit within token limits
type DiffChunk struct {
	Files       []FileDiff
	ChunkID     int
	TotalChunks int
	TokenCount  int
}

// DiffSplitter splits large diffs into manageable chunks
type DiffSplitter struct {
	MaxTokensPerChunk int
	MaxFilesPerChunk  int
}

// NewDiffSplitter creates a new DiffSplitter with default settings
func NewDiffSplitter(maxTokens, maxFiles int) *DiffSplitter {
	if maxTokens <= 0 {
		maxTokens = 40000
	}
	if maxFiles <= 0 {
		maxFiles = 10
	}
	return &DiffSplitter{
		MaxTokensPerChunk: maxTokens,
		MaxFilesPerChunk:  maxFiles,
	}
}

// Split parses a unified diff and splits it into chunks
func (s *DiffSplitter) Split(fullDiff string) []DiffChunk {
	files := s.parseFiles(fullDiff)
	if len(files) == 0 {
		return nil
	}
	slog.Debug("Parsed files", "count", len(files))
	for _, f := range files {
		slog.Debug("File", "path", f.Path, "size", len(f.Content), "tokens", f.Tokens)
	}

	return s.groupIntoChunks(files)
}

// parseFiles extracts individual file diffs from a unified diff
func (s *DiffSplitter) parseFiles(fullDiff string) []FileDiff {
	// Match diff headers: "diff --git a/path b/path" or "--- a/path"
	// Match diff headers anywhere a line starts with it
	diffPattern := regexp.MustCompile(`(?m)diff --git\s+(.+?)\s+(\S+)`)
	matches := diffPattern.FindAllStringSubmatchIndex(fullDiff, -1)

	if len(matches) == 0 {
		// Fallback: try simpler pattern
		return s.parseSimpleDiff(fullDiff)
	}

	var files []FileDiff
	for i, match := range matches {
		start := match[0]
		end := len(fullDiff)
		if i+1 < len(matches) {
			end = matches[i+1][0]
		}

		content := fullDiff[start:end]
		path := fullDiff[match[4]:match[5]] // Second capture group
		path = strings.TrimPrefix(path, "b/")
		path = strings.TrimPrefix(path, "dst/trunk/")
		path = strings.TrimPrefix(path, "dst://trunk/")

		files = append(files, FileDiff{
			Path:    path,
			Content: content,
			Tokens:  estimateTokens(content),
		})
	}

	return files
}

// parseSimpleDiff handles simpler diff formats
func (s *DiffSplitter) parseSimpleDiff(fullDiff string) []FileDiff {
	// Split by "--- " which marks file boundaries
	pattern := regexp.MustCompile(`(?m)^--- (?:[^\s]+?)/(.+)$`)
	matches := pattern.FindAllStringSubmatchIndex(fullDiff, -1)

	if len(matches) == 0 {
		// Can't parse, return as single chunk
		return []FileDiff{{
			Path:    "unknown",
			Content: fullDiff,
			Tokens:  estimateTokens(fullDiff),
		}}
	}

	var files []FileDiff
	for i, match := range matches {
		start := match[0]
		end := len(fullDiff)
		if i+1 < len(matches) {
			end = matches[i+1][0]
		}

		content := fullDiff[start:end]
		path := strings.TrimSpace(fullDiff[match[2]:match[3]])
		path = strings.TrimPrefix(path, "a/")
		path = strings.TrimPrefix(path, "src/trunk/")
		path = strings.TrimPrefix(path, "src://trunk/")

		files = append(files, FileDiff{
			Path:    path,
			Content: content,
			Tokens:  estimateTokens(content),
		})
	}

	return files
}

// groupIntoChunks uses greedy algorithm to group files into chunks
func (s *DiffSplitter) groupIntoChunks(files []FileDiff) []DiffChunk {
	var chunks []DiffChunk
	var currentFiles []FileDiff
	currentTokens := 0

	for _, file := range files {
		// Check if adding this file would exceed limits
		wouldExceedTokens := currentTokens+file.Tokens > s.MaxTokensPerChunk
		wouldExceedFiles := len(currentFiles) >= s.MaxFilesPerChunk

		if (wouldExceedTokens || wouldExceedFiles) && len(currentFiles) > 0 {
			// Finalize current chunk
			chunks = append(chunks, DiffChunk{
				Files:      currentFiles,
				TokenCount: currentTokens,
			})
			currentFiles = nil
			currentTokens = 0
		}

		// Handle oversized single file
		if file.Tokens > s.MaxTokensPerChunk {
			// Split large file into sub-chunks
			subChunks := s.splitLargeFile(file)
			for _, sc := range subChunks {
				chunks = append(chunks, DiffChunk{
					Files:      []FileDiff{sc},
					TokenCount: sc.Tokens,
				})
			}
			continue
		}

		currentFiles = append(currentFiles, file)
		currentTokens += file.Tokens
	}

	// Don't forget the last chunk
	if len(currentFiles) > 0 {
		chunks = append(chunks, DiffChunk{
			Files:      currentFiles,
			TokenCount: currentTokens,
		})
	}

	// Set chunk IDs
	total := len(chunks)
	for i := range chunks {
		chunks[i].ChunkID = i + 1
		chunks[i].TotalChunks = total
	}

	return chunks
}

// splitLargeFile splits an oversized file diff into smaller pieces
func (s *DiffSplitter) splitLargeFile(file FileDiff) []FileDiff {
	lines := strings.Split(file.Content, "\n")
	var result []FileDiff

	linesPerChunk := len(lines) * s.MaxTokensPerChunk / file.Tokens
	if linesPerChunk < 50 {
		linesPerChunk = 50
	}

	for i := 0; i < len(lines); i += linesPerChunk {
		end := i + linesPerChunk
		if end > len(lines) {
			end = len(lines)
		}

		content := strings.Join(lines[i:end], "\n")
		result = append(result, FileDiff{
			Path:    file.Path,
			Content: content,
			Tokens:  estimateTokens(content),
		})
	}

	return result
}

// estimateTokens estimates token count (roughly 4 chars per token)
func estimateTokens(text string) int {
	return len(text) / 4
}

// CombineChunkContent creates a single diff string from a chunk
func (c *DiffChunk) CombineContent() string {
	var sb strings.Builder
	for _, f := range c.Files {
		sb.WriteString(f.Content)
		sb.WriteString("\n")
	}
	return sb.String()
}

// FileList returns a list of file paths in this chunk
func (c *DiffChunk) FileList() []string {
	paths := make([]string, len(c.Files))
	for i, f := range c.Files {
		paths[i] = f.Path
	}
	return paths
}
