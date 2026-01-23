package splitter

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
	files := p.splitByFile(diff)

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

// splitByFile splits a unified diff into per-file sections
func (p *DiffPreprocessor) splitByFile(diff string) []string {
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
		path := p.extractFilePath(fileDiff)
		return "diff --git a/" + path + " b/" + path + "\n[BINARY FILE - SKIPPED]\n"
	}

	// Check for pure whitespace changes
	if p.opts.RemoveWhitespace && p.isPureWhitespaceChange(fileDiff) {
		path := p.extractFilePath(fileDiff)
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
			result = append(result, "- [... "+formatInt(len(deleteBuffer))+" lines deleted ...]")
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

// extractFilePath extracts the file path from a diff header
func (p *DiffPreprocessor) extractFilePath(fileDiff string) string {
	// Match both standard a/b paths and custom prefixes like src:// dst://
	// Patterns:
	// diff --git a/path b/path
	// diff --git src://path dst://path
	// --- a/path
	// +++ b/path

	// Try standard git diff first
	pattern := regexp.MustCompile(`diff --git\s+\S+\s+(?:b/|dst://|)(\S+)`)
	match := pattern.FindStringSubmatch(fileDiff)
	if len(match) > 1 {
		return match[1]
	}

	// Try ---/+++ headers
	pattern = regexp.MustCompile(`(?m)^\+\+\+\s+(?:b/|dst://|)(\S+)`)
	match = pattern.FindStringSubmatch(fileDiff)
	if len(match) > 1 {
		return match[1]
	}

	return "unknown"
}

// formatInt converts int to string (avoids importing strconv for simple case)
func formatInt(n int) string {
	if n == 0 {
		return "0"
	}

	var result []byte
	negative := n < 0
	if negative {
		n = -n
	}

	for n > 0 {
		result = append([]byte{byte('0' + n%10)}, result...)
		n /= 10
	}

	if negative {
		result = append([]byte{'-'}, result...)
	}

	return string(result)
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
