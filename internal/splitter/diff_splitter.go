package splitter

import (
	"log/slog"
	"pr-review-automation/internal/config"
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
	ContextLines      int // Context lines to preserve when splitting large files
}

// NewDiffSplitter creates a new DiffSplitter with default settings
func NewDiffSplitter(maxTokens, maxFiles int) *DiffSplitter {
	return NewDiffSplitterWithContext(maxTokens, maxFiles, 20)
}

// NewDiffSplitterWithContext creates a new DiffSplitter with context line configuration
func NewDiffSplitterWithContext(maxTokens, maxFiles, contextLines int) *DiffSplitter {
	if maxTokens <= 0 {
		maxTokens = 40000
	}
	if maxFiles <= 0 {
		maxFiles = 10
	}
	if contextLines <= 0 {
		contextLines = 20
	}
	return &DiffSplitter{
		MaxTokensPerChunk: maxTokens,
		MaxFilesPerChunk:  maxFiles,
		ContextLines:      contextLines,
	}
}

// Split parses a unified diff and splits it into chunks
// Strategy: Prioritize file boundaries, then split by hunks with context preservation
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
	// Match diff headers: "diff --git a/path b/path" or "diff --git src://trunk/path dst://trunk/path"
	// Captures destination path (second path in the header)
	diffPattern := regexp.MustCompile(`(?m)^diff --git\s+\S+\s+(\S+?)(?:\s|$)`)
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
		path := fullDiff[match[2]:match[3]] // First capture group (b/path)
		path = strings.TrimPrefix(path, config.PathPrefixGitDestination)
		path = strings.TrimPrefix(path, config.PathPrefixSVNDest)
		path = strings.TrimPrefix(path, config.PathPrefixSVNDestURI)

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
		path = strings.TrimPrefix(path, config.PathPrefixGitSource)
		path = strings.TrimPrefix(path, config.PathPrefixSVNSource)
		path = strings.TrimPrefix(path, config.PathPrefixSVNSourceURI)

		files = append(files, FileDiff{
			Path:    path,
			Content: content,
			Tokens:  estimateTokens(content),
		})
	}

	return files
}

// groupIntoChunks groups files into chunks, prioritizing file boundaries
// Strategy:
// 1. Each file stays intact if it fits within token limit
// 2. If a file exceeds limit, split by hunks with context preservation
// 3. Group small files together up to the limit
func (s *DiffSplitter) groupIntoChunks(files []FileDiff) []DiffChunk {
	var chunks []DiffChunk
	var currentFiles []FileDiff
	currentTokens := 0

	for _, file := range files {
		// Handle oversized single file first
		if file.Tokens > s.MaxTokensPerChunk {
			// Finalize current chunk before splitting large file
			if len(currentFiles) > 0 {
				chunks = append(chunks, DiffChunk{
					Files:      currentFiles,
					TokenCount: currentTokens,
				})
				currentFiles = nil
				currentTokens = 0
			}

			// Split large file by hunks with context preservation
			subChunks := s.splitLargeFileByHunks(file)
			for _, sc := range subChunks {
				chunks = append(chunks, DiffChunk{
					Files:      []FileDiff{sc},
					TokenCount: sc.Tokens,
				})
			}
			continue
		}

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

// splitLargeFileByHunks splits an oversized file diff by hunk boundaries
// Each sub-chunk includes context lines for better understanding
func (s *DiffSplitter) splitLargeFileByHunks(file FileDiff) []FileDiff {
	hunks := s.parseHunks(file.Content)
	if len(hunks) == 0 {
		// Fallback to line-based splitting
		return s.splitLargeFileByLines(file)
	}

	// Extract file header (everything before first hunk)
	fileHeader := s.extractFileHeader(file.Content)

	var result []FileDiff
	var currentHunks []string
	currentTokens := estimateTokens(fileHeader)

	for _, hunk := range hunks {
		hunkTokens := estimateTokens(hunk)

		// If single hunk is too large, split it further
		if hunkTokens > s.MaxTokensPerChunk-estimateTokens(fileHeader) {
			// Finalize current chunk
			if len(currentHunks) > 0 {
				content := fileHeader + strings.Join(currentHunks, "\n")
				result = append(result, FileDiff{
					Path:    file.Path,
					Content: content,
					Tokens:  estimateTokens(content),
				})
				currentHunks = nil
				currentTokens = estimateTokens(fileHeader)
			}

			// Split the large hunk by lines
			subHunks := s.splitLargeHunk(hunk, s.MaxTokensPerChunk-estimateTokens(fileHeader))
			for _, sh := range subHunks {
				content := fileHeader + sh
				result = append(result, FileDiff{
					Path:    file.Path,
					Content: content,
					Tokens:  estimateTokens(content),
				})
			}
			continue
		}

		// Check if adding this hunk would exceed limit
		if currentTokens+hunkTokens > s.MaxTokensPerChunk && len(currentHunks) > 0 {
			// Finalize current chunk
			content := fileHeader + strings.Join(currentHunks, "\n")
			result = append(result, FileDiff{
				Path:    file.Path,
				Content: content,
				Tokens:  estimateTokens(content),
			})
			currentHunks = nil
			currentTokens = estimateTokens(fileHeader)
		}

		currentHunks = append(currentHunks, hunk)
		currentTokens += hunkTokens
	}

	// Don't forget the last chunk
	if len(currentHunks) > 0 {
		content := fileHeader + strings.Join(currentHunks, "\n")
		result = append(result, FileDiff{
			Path:    file.Path,
			Content: content,
			Tokens:  estimateTokens(content),
		})
	}

	slog.Debug("Split large file by hunks",
		"path", file.Path,
		"original_tokens", file.Tokens,
		"chunks", len(result))

	return result
}

// parseHunks extracts individual hunks from a file diff
// A hunk starts with @@ and ends before the next @@ or EOF
func (s *DiffSplitter) parseHunks(content string) []string {
	hunkPattern := regexp.MustCompile(`(?m)^@@[^@]+@@`)
	matches := hunkPattern.FindAllStringIndex(content, -1)

	if len(matches) == 0 {
		return nil
	}

	var hunks []string
	for i, match := range matches {
		start := match[0]
		end := len(content)
		if i+1 < len(matches) {
			end = matches[i+1][0]
		}
		hunks = append(hunks, strings.TrimRight(content[start:end], "\n"))
	}

	return hunks
}

// extractFileHeader extracts the header portion of a file diff (before first hunk)
func (s *DiffSplitter) extractFileHeader(content string) string {
	hunkPattern := regexp.MustCompile(`(?m)^@@`)
	loc := hunkPattern.FindStringIndex(content)
	if loc == nil {
		return content
	}
	return content[:loc[0]]
}

// splitLargeHunk splits a single large hunk into smaller pieces with context
func (s *DiffSplitter) splitLargeHunk(hunk string, maxTokens int) []string {
	lines := strings.Split(hunk, "\n")
	if len(lines) == 0 {
		return nil
	}

	// Keep hunk header
	hunkHeader := ""
	startLine := 0
	if len(lines) > 0 && strings.HasPrefix(lines[0], "@@") {
		hunkHeader = lines[0] + "\n"
		startLine = 1
	}

	var result []string
	headerTokens := estimateTokens(hunkHeader)
	contextTokens := s.ContextLines * 10 // Rough estimate: 10 tokens per context line

	// Calculate lines per sub-chunk
	availableTokens := maxTokens - headerTokens - contextTokens*2
	if availableTokens < 100 {
		availableTokens = 100
	}
	tokensPerLine := 10 // Rough estimate
	linesPerChunk := availableTokens / tokensPerLine
	if linesPerChunk < 20 {
		linesPerChunk = 20
	}

	for i := startLine; i < len(lines); {
		end := i + linesPerChunk
		if end > len(lines) {
			end = len(lines)
		}

		// Build sub-chunk with context
		var chunkLines []string

		// Add leading context (from previous chunk if not first)
		if i > startLine {
			contextStart := i - s.ContextLines
			if contextStart < startLine {
				contextStart = startLine
			}
			for j := contextStart; j < i; j++ {
				// Mark as context line
				line := lines[j]
				if !strings.HasPrefix(line, "+") && !strings.HasPrefix(line, "-") && !strings.HasPrefix(line, " ") {
					line = " " + line
				}
				chunkLines = append(chunkLines, line)
			}
		}

		// Add main content
		chunkLines = append(chunkLines, lines[i:end]...)

		// Add trailing context
		if end < len(lines) {
			contextEnd := end + s.ContextLines
			if contextEnd > len(lines) {
				contextEnd = len(lines)
			}
			for j := end; j < contextEnd; j++ {
				line := lines[j]
				if !strings.HasPrefix(line, "+") && !strings.HasPrefix(line, "-") && !strings.HasPrefix(line, " ") {
					line = " " + line
				}
				chunkLines = append(chunkLines, line)
			}
		}

		content := hunkHeader + strings.Join(chunkLines, "\n")
		result = append(result, content)

		i = end
	}

	return result
}

// splitLargeFileByLines is the fallback method when hunk parsing fails
func (s *DiffSplitter) splitLargeFileByLines(file FileDiff) []FileDiff {
	lines := strings.Split(file.Content, "\n")
	var result []FileDiff

	linesPerChunk := len(lines) * s.MaxTokensPerChunk / file.Tokens
	if linesPerChunk < 50 {
		linesPerChunk = 50
	}

	for i := 0; i < len(lines); {
		end := i + linesPerChunk
		if end > len(lines) {
			end = len(lines)
		}

		// Add context lines
		actualStart := i
		if i > 0 {
			actualStart = i - s.ContextLines
			if actualStart < 0 {
				actualStart = 0
			}
		}

		actualEnd := end
		if end < len(lines) {
			actualEnd = end + s.ContextLines
			if actualEnd > len(lines) {
				actualEnd = len(lines)
			}
		}

		content := strings.Join(lines[actualStart:actualEnd], "\n")
		result = append(result, FileDiff{
			Path:    file.Path,
			Content: content,
			Tokens:  estimateTokens(content),
		})

		i = end
	}

	return result
}

// estimateTokens estimates token count (roughly 4 chars per token)
func estimateTokens(text string) int {
	return len(text) / 4
}

// CombineContent creates a single diff string from a chunk
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
