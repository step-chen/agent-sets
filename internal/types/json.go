package types

import "strings"

// CleanJSONFromMarkdown removes markdown code block wrappers from JSON strings.
// This is commonly needed when parsing LLM responses that may include markdown formatting.
func CleanJSONFromMarkdown(s string) string {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "```json")
	s = strings.TrimPrefix(s, "```")
	s = strings.TrimSuffix(s, "```")
	return strings.TrimSpace(s)
}
