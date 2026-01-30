package validator

import (
	"pr-review-automation/internal/domain"
	"regexp"
	"strconv"
	"strings"
)

// LineRange represents a range of valid lines in a file
type LineRange struct {
	Start int
	End   int
}

// CommentValidator validates AI comments against diff ranges
// It ensures comments only target lines that were actually modified (+ lines in diff)
type CommentValidator struct {
	validRanges map[string][]LineRange    // file -> valid line ranges (only + lines)
	lineTypes   map[string]map[int]string // file -> line -> type (ADDED/CONTEXT)
	allFiles    map[string]bool           // all files in diff
}

// NewCommentValidator creates a validator from a unified diff string
func NewCommentValidator(diff string) *CommentValidator {
	v := &CommentValidator{
		validRanges: make(map[string][]LineRange),
		lineTypes:   make(map[string]map[int]string),
		allFiles:    make(map[string]bool),
	}
	v.parseDiff(diff)
	return v
}

// parseDiff extracts valid line ranges from unified diff
// Only lines starting with + (excluding +++ header) are valid for inline comments
func (v *CommentValidator) parseDiff(diff string) {
	// Match file headers: "diff --git a/path b/path" or "+++ b/path"
	filePattern := regexp.MustCompile(`(?m)^\+\+\+ (?:b/)?(.+)$`)
	// Match hunk headers: @@ -start,count +start,count @@
	hunkPattern := regexp.MustCompile(`(?m)^@@ -\d+(?:,\d+)? \+(\d+)(?:,(\d+))? @@`)

	lines := strings.Split(diff, "\n")
	var currentFile string
	var currentLineNum int
	var inHunk bool

	for _, line := range lines {
		// Check for new file
		if matches := filePattern.FindStringSubmatch(line); len(matches) > 1 {
			currentFile = v.normalizeFilePath(strings.TrimSpace(matches[1]))
			v.allFiles[currentFile] = true
			if _, ok := v.lineTypes[currentFile]; !ok {
				v.lineTypes[currentFile] = make(map[int]string)
			}
			inHunk = false
			continue
		}

		// Check for hunk header
		if matches := hunkPattern.FindStringSubmatch(line); len(matches) > 1 {
			startLine, _ := strconv.Atoi(matches[1])
			currentLineNum = startLine
			inHunk = true
			continue
		}

		if !inHunk || currentFile == "" {
			continue
		}

		// Process diff lines
		if strings.HasPrefix(line, "+") && !strings.HasPrefix(line, "+++") {
			// This is an added/modified line - valid for comments
			v.addValidLine(currentFile, currentLineNum)
			v.lineTypes[currentFile][currentLineNum] = "ADDED"
			currentLineNum++
		} else if strings.HasPrefix(line, "-") && !strings.HasPrefix(line, "---") {
			// Deleted line - doesn't increment new file line number
		} else if strings.HasPrefix(line, " ") || line == "" {
			// Context line - increment line number and type as CONTEXT
			v.addValidLine(currentFile, currentLineNum)
			v.lineTypes[currentFile][currentLineNum] = "CONTEXT"
			currentLineNum++
		}
	}
}

// GetLineType returns the type of the line (ADDED or CONTEXT) if available
func (v *CommentValidator) GetLineType(file string, line int) string {
	normalizedFile := v.normalizeFilePath(file)
	if types, ok := v.lineTypes[normalizedFile]; ok {
		if t, ok := types[line]; ok {
			return t
		}
	}

	// Fallback to partial match if exact file match fails
	for f, types := range v.lineTypes {
		if strings.HasSuffix(f, normalizedFile) || strings.HasSuffix(normalizedFile, f) {
			if t, ok := types[line]; ok {
				return t
			}
		}
	}

	return ""
}

// addValidLine adds a line to the valid ranges, merging adjacent ranges
func (v *CommentValidator) addValidLine(file string, line int) {
	ranges := v.validRanges[file]

	// Try to extend existing range
	for i := range ranges {
		if line == ranges[i].End+1 {
			ranges[i].End = line
			v.validRanges[file] = ranges
			return
		}
		if line >= ranges[i].Start && line <= ranges[i].End {
			return // Already in range
		}
	}

	// Add new range
	v.validRanges[file] = append(ranges, LineRange{Start: line, End: line})
}

// IsValid checks if a comment on the given file and line is valid
func (v *CommentValidator) IsValid(file string, line int) bool {
	// Normalize file path (remove leading slashes, handle different formats)
	normalizedFile := v.normalizeFilePath(file)

	ranges, ok := v.validRanges[normalizedFile]
	if !ok {
		// Try to find partial match (file might have different path prefix)
		for f, r := range v.validRanges {
			if strings.HasSuffix(f, "/"+normalizedFile) || strings.HasSuffix(normalizedFile, "/"+f) ||
				strings.HasSuffix(f, normalizedFile) || strings.HasSuffix(normalizedFile, f) {
				ranges = r
				ok = true
				break
			}
		}
	}

	if !ok {
		return false
	}

	for _, r := range ranges {
		if line >= r.Start && line <= r.End {
			return true
		}
	}
	return false
}

// FileInDiff checks if the file is part of the diff at all
func (v *CommentValidator) FileInDiff(file string) bool {
	normalizedFile := v.normalizeFilePath(file)

	if v.allFiles[normalizedFile] {
		return true
	}

	// Try partial match
	for f := range v.allFiles {
		if strings.HasSuffix(f, "/"+normalizedFile) || strings.HasSuffix(normalizedFile, "/"+f) ||
			strings.HasSuffix(f, normalizedFile) || strings.HasSuffix(normalizedFile, f) {
			return true
		}
	}
	return false
}

// GetInvalidReason returns a human-readable reason why the comment is invalid
func (v *CommentValidator) GetInvalidReason(file string, line int) string {
	if !v.FileInDiff(file) {
		return "file not in diff"
	}

	normalizedFile := v.normalizeFilePath(file)
	ranges := v.validRanges[normalizedFile]
	if len(ranges) == 0 {
		// Find ranges via partial match
		for f, r := range v.validRanges {
			if strings.HasSuffix(f, normalizedFile) || strings.HasSuffix(normalizedFile, f) {
				ranges = r
				break
			}
		}
	}

	if len(ranges) == 0 {
		return "no modified lines in file"
	}

	// Find nearest valid range
	var nearestRange LineRange
	minDist := int(^uint(0) >> 1) // Max int
	for _, r := range ranges {
		if dist := abs(line - r.Start); dist < minDist {
			minDist = dist
			nearestRange = r
		}
		if dist := abs(line - r.End); dist < minDist {
			minDist = dist
			nearestRange = r
		}
	}

	return "line not modified in diff (nearest: " + strconv.Itoa(nearestRange.Start) + "-" + strconv.Itoa(nearestRange.End) + ")"
}

// GetValidRanges returns all valid ranges for a file
func (v *CommentValidator) GetValidRanges(file string) []LineRange {
	normalizedFile := v.normalizeFilePath(file)
	if ranges, ok := v.validRanges[normalizedFile]; ok {
		return ranges
	}

	// Try partial match
	for f, r := range v.validRanges {
		if strings.HasSuffix(f, normalizedFile) || strings.HasSuffix(normalizedFile, f) {
			return r
		}
	}
	return nil
}

// normalizeFilePath normalizes file paths for comparison
var (
	markdownLinkRegex = regexp.MustCompile(`^\[(.*?)\]\(.*?\)$`)
	urlPrefixRegex    = regexp.MustCompile(`^(?:tree|blob)/[^/]+/`)
)

func (v *CommentValidator) normalizeFilePath(file string) string {
	// 1. Strip Markdown link: [file.go](...) -> file.go
	if matches := markdownLinkRegex.FindStringSubmatch(file); len(matches) > 1 {
		file = matches[1]
	}

	// 2. Standardize separators to forward slashes
	file = strings.ReplaceAll(file, "\\", "/")

	// 3. Strip common URL prefixes (e.g. tree/main/, blob/master/)
	file = urlPrefixRegex.ReplaceAllString(file, "")

	return domain.NormalizePath(file)
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}
