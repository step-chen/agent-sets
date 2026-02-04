package processor

import (
	"fmt"
	"regexp"
)

// TransformLinks replaces relative links with absolute Bitbucket links
// Input:  [path:line](path#Lline) or [path](path)
// Output: [path:line](BASE_URL/diff#path?t=line) or [path](BASE_URL/diff#path)
func TransformLinks(text, prWebURL string) string {
	if prWebURL == "" {
		return text
	}

	// Pattern 1: [任意文本](相对路径#L行号)
	// Example: [src/main.cpp:10](src/main.cpp#L10) -> [src/main.cpp:10](PR_URL/diff#src/main.cpp?t=10)
	// Group 1: Label text (e.g. "src/main.cpp:10")
	// Group 2: File path (e.g. "src/main.cpp")
	// Group 3: Line number (e.g. "10")
	reLine := regexp.MustCompile(`\[([^\]]+)\]\(([^)#]+)#L(\d+)\)`)
	text = reLine.ReplaceAllStringFunc(text, func(match string) string {
		parts := reLine.FindStringSubmatch(match)
		if len(parts) < 4 {
			return match
		}
		label, path, line := parts[1], parts[2], parts[3]
		return fmt.Sprintf("[%s](%s/diff#%s?t=%s)", label, prWebURL, path, line)
	})

	// Pattern 2: [任意文本](相对路径) - File Only Link
	// Example: [src/main.cpp](src/main.cpp) -> [src/main.cpp](PR_URL/diff#src/main.cpp)
	// We must be careful not to match standard web links (http/https).
	// We assume relative paths don't start with http/https
	reFile := regexp.MustCompile(`\[([^\]]+)\]\(([^)]+)\)`)
	text = reFile.ReplaceAllStringFunc(text, func(match string) string {
		parts := reFile.FindStringSubmatch(match)
		if len(parts) < 3 {
			return match
		}
		label, path := parts[1], parts[2]

		// Skip if it looks like a full URL or contains #L (already handled) or is an anchor
		if regexp.MustCompile(`^(http|https|mailto):`).MatchString(path) ||
			regexp.MustCompile(`#L\d+$`).MatchString(path) ||
			regexp.MustCompile(`^#`).MatchString(path) {
			return match
		}

		return fmt.Sprintf("[%s](%s/diff#%s)", label, prWebURL, path)
	})

	return text
}
