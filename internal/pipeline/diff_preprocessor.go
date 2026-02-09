package pipeline

import (
	"regexp"
	"strconv"
	"strings"
)

// PreprocessOptions configures diff preprocessing behavior
type PreprocessOptions struct {
	MaxContextLines  int      // Max consecutive context lines to keep (default: 5)
	FoldDeletesOver  int      // Fold consecutive deletes over N lines into summary (default: 30)
	RemoveBinaryDiff bool     // Remove binary file diffs (default: true)
	RemoveWhitespace bool     // Remove pure whitespace changes (default: true)
	CompressSpaces   bool     // Compress consecutive spaces to single space (default: true)
	IgnorePatterns   []string // File patterns to ignore (not implemented yet)
}

// DefaultPreprocessOptions returns sensible defaults
func DefaultPreprocessOptions() PreprocessOptions {
	return PreprocessOptions{
		MaxContextLines:  5,
		FoldDeletesOver:  30,
		RemoveBinaryDiff: true,
		RemoveWhitespace: true,
		CompressSpaces:   true,
	}
}

// DiffPreprocessor preprocesses diffs to reduce token usage
type DiffPreprocessor struct {
	opts PreprocessOptions
}

// NewDiffPreprocessor creates a new preprocessor with given options
func NewDiffPreprocessor(opts PreprocessOptions) *DiffPreprocessor {
	if opts.MaxContextLines <= 0 {
		opts.MaxContextLines = 5
	}
	if opts.FoldDeletesOver <= 0 {
		opts.FoldDeletesOver = 30
	}
	return &DiffPreprocessor{opts: opts}
}

// Preprocess processes a full diff to reduce token usage
func (p *DiffPreprocessor) Preprocess(diff string) string {
	// Split by file
	files := p.SplitByFile(diff)

	var result []string
	for _, file := range files {
		processed := p.processFile(file)
		if processed != "" {
			result = append(result, processed)
		}
	}

	output := strings.Join(result, "\n")

	// Compress consecutive spaces if enabled
	if p.opts.CompressSpaces {
		output = p.compressSpaces(output)
	}

	return output
}

// SplitByFile splits a unified diff into per-file sections
func (p *DiffPreprocessor) SplitByFile(diff string) []string {
	pattern := regexp.MustCompile(`(?m)^diff --git`)
	indices := pattern.FindAllStringIndex(diff, -1)

	if len(indices) == 0 {
		return []string{diff}
	}

	var files []string
	for i, idx := range indices {
		start := idx[0]
		end := len(diff)
		if i+1 < len(indices) {
			end = indices[i+1][0]
		}
		files = append(files, diff[start:end])
	}

	return files
}

// processFile processes a single file diff
func (p *DiffPreprocessor) processFile(fileDiff string) string {
	// Check for binary file
	if p.opts.RemoveBinaryDiff && p.isBinaryDiff(fileDiff) {
		// Extract file path and return a summary
		path := p.ExtractFilePath(fileDiff)
		return "diff --git a/" + path + " b/" + path + "\n[BINARY FILE - SKIPPED]\n"
	}

	// Check for pure whitespace changes
	if p.opts.RemoveWhitespace && p.isPureWhitespaceChange(fileDiff) {
		path := p.ExtractFilePath(fileDiff)
		return "diff --git a/" + path + " b/" + path + "\n[WHITESPACE ONLY - SKIPPED]\n"
	}

	// Process line by line
	lines := strings.Split(fileDiff, "\n")
	var result []string

	consecutiveContext := 0
	consecutiveDeletes := 0
	deleteBuffer := []string{}

	for _, line := range lines {
		// Detect line type
		isContext := len(line) > 0 && line[0] == ' '
		isDelete := len(line) > 0 && line[0] == '-' && !strings.HasPrefix(line, "---")
		isAdd := len(line) > 0 && line[0] == '+' && !strings.HasPrefix(line, "+++")
		isHeader := strings.HasPrefix(line, "diff ") ||
			strings.HasPrefix(line, "index ") ||
			strings.HasPrefix(line, "---") ||
			strings.HasPrefix(line, "+++") ||
			strings.HasPrefix(line, "@@")

		// Handle consecutive deletes folding
		if isDelete {
			consecutiveDeletes++
			deleteBuffer = append(deleteBuffer, line)
			consecutiveContext = 0
			continue
		} else if len(deleteBuffer) > 0 {
			// Flush delete buffer
			if len(deleteBuffer) > p.opts.FoldDeletesOver {
				result = append(result, "- [... "+strconv.Itoa(len(deleteBuffer))+" lines deleted ...]")
			} else {
				result = append(result, deleteBuffer...)
			}
			deleteBuffer = nil
			consecutiveDeletes = 0
		}

		// Handle context line compression
		if isContext {
			consecutiveContext++
			if consecutiveContext <= p.opts.MaxContextLines {
				result = append(result, line)
			} else if consecutiveContext == p.opts.MaxContextLines+1 {
				result = append(result, " [... context lines omitted ...]")
			}
			// Skip additional context lines
			continue
		} else {
			consecutiveContext = 0
		}

		// Always keep headers and additions
		if isHeader || isAdd || !isContext {
			result = append(result, line)
		}
	}

	// Flush remaining delete buffer
	if len(deleteBuffer) > 0 {
		if len(deleteBuffer) > p.opts.FoldDeletesOver {
			result = append(result, "- [... "+strconv.Itoa(len(deleteBuffer))+" lines deleted ...]")
		} else {
			result = append(result, deleteBuffer...)
		}
	}

	return strings.Join(result, "\n")
}

// isBinaryDiff checks if a file diff is for a binary file
func (p *DiffPreprocessor) isBinaryDiff(fileDiff string) bool {
	return strings.Contains(fileDiff, "Binary files") ||
		strings.Contains(fileDiff, "GIT binary patch")
}

// isPureWhitespaceChange checks if a diff only contains whitespace changes
func (p *DiffPreprocessor) isPureWhitespaceChange(fileDiff string) bool {
	fileDiff = strings.ReplaceAll(fileDiff, "\r\n", "\n")
	lines := strings.Split(fileDiff, "\n")
	hasNonWhitespaceChange := false
	// hasRealChangeLine := false

	for _, line := range lines {
		trimmedLine := strings.TrimSpace(line)
		if trimmedLine == "" {
			continue
		}

		// Skip headers
		if strings.HasPrefix(line, "diff ") ||
			strings.HasPrefix(line, "index ") ||
			strings.HasPrefix(line, "---") ||
			strings.HasPrefix(line, "+++") ||
			strings.HasPrefix(line, "@@") {
			continue
		}

		// Check add/delete lines
		if line[0] == '+' || line[0] == '-' {
			content := line[1:]
			// If trimmed content is non-empty, it's not pure whitespace
			if strings.TrimSpace(content) != "" {
				hasNonWhitespaceChange = true
				break
			}
		}
	}

	return !hasNonWhitespaceChange
}

// ExtractFilePath extracts the file path from a diff header using a state-machine approach.
// It prioritizes specific git headers (+++, rename to) over the generic diff command.
func (p *DiffPreprocessor) ExtractFilePath(fileDiff string) string {
	var fallback string

	// Only scan the header section (limit to 15 lines)
	// This avoids scanning massive files but covers all standard git headers
	headerLines := strings.SplitN(fileDiff, "\n", 15)

	for _, line := range headerLines {
		// Stop at hunk header or binary marker
		if strings.HasPrefix(line, "@@ ") || strings.HasPrefix(line, "Binary files ") {
			break
		}

		// 1. Priority: +++ line (Target Path)
		// Format: +++ b/path/to/file.go
		if strings.HasPrefix(line, "+++ ") {
			// Extract path after "+++ " prefix
			if path := extractPath(line[4:]); path != "/dev/null" {
				return path
			}
			continue
		}

		// 2. Priority: Rename/Copy (Target Path)
		// Format: rename to path/to/file.go
		if strings.HasPrefix(line, "rename to ") {
			return extractPath(line[10:])
		}
		if strings.HasPrefix(line, "copy to ") {
			return extractPath(line[8:])
		}

		// 3. Fallback: diff --git line
		// Format: diff --git a/src b/dst
		if fallback == "" && strings.HasPrefix(line, "diff --git ") {
			fallback = extractDstPath(line)
		}
	}

	if fallback != "" {
		return fallback
	}

	return "unknown"
}

// extractPath cleans path prefixes (b/, dst://, a/, src://) and unquotes if necessary
func extractPath(s string) string {
	s = strings.TrimSpace(s)
	s = strings.Trim(s, "\"")
	for _, prefix := range []string{"b/", "a/", "dst://", "src://"} {
		s = strings.TrimPrefix(s, prefix)
	}
	return s
}

// extractDstPath extracts destination path from "diff --git a/x b/y"
func extractDstPath(line string) string {
	// Quoted paths: diff --git "a/x" "b/y"
	// Find the separator between two quoted strings
	if i := strings.LastIndex(line, "\" \""); i > 0 {
		return extractPath(line[i+3:])
	}

	// Unquoted paths: diff --git a/x b/y
	// Git allows spaces in unquoted paths only if they are escaped, but usually quotes them.
	// Standard git diff output for unquoted paths doesn't have spaces.
	// We take the last field as destination.
	parts := strings.Fields(line)
	if len(parts) >= 4 {
		return extractPath(parts[len(parts)-1])
	}
	return ""
}

// compressSpaces compresses consecutive spaces/tabs to single space
func (p *DiffPreprocessor) compressSpaces(input string) string {
	// Use regex to replace multiple spaces/tabs with single space
	// But preserve leading indentation (first occurrence of whitespace at line start)
	lines := strings.Split(input, "\n")
	var result []string

	spacePattern := regexp.MustCompile(`[ \t]{2,}`)

	for _, line := range lines {
		if len(line) == 0 {
			result = append(result, line)
			continue
		}

		// Find leading whitespace
		leadingSpaces := 0
		for i, ch := range line {
			if ch == ' ' || ch == '\t' {
				leadingSpaces = i + 1
			} else {
				break
			}
		}

		// Keep leading whitespace, compress the rest
		if leadingSpaces > 0 && leadingSpaces < len(line) {
			leading := line[:leadingSpaces]
			rest := line[leadingSpaces:]
			rest = spacePattern.ReplaceAllString(rest, " ")
			result = append(result, leading+rest)
		} else {
			// No leading whitespace or entire line is whitespace
			result = append(result, spacePattern.ReplaceAllString(line, " "))
		}
	}

	return strings.Join(result, "\n")
}
