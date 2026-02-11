package pipeline

import (
	"regexp"
)

// RepairJSON attempts to repair common JSON format errors
// Currently mainly targets: missing commas between fields or array elements
func RepairJSON(s string) string {
	// 1. Repair missing commas between object/array elements
	// Scenario: "key": "value" "next": 1
	// Pattern: (End of value) + whitespace + "
	// Note: Go regex does not support lookaround, so we use capturing groups

	// Match: Ends with " } ] number or letter
	// And followed by " { [ number or letter (start of next value)
	// Replace with: $1,$2

	// Regex explanation:
	// (["}\]0-9a-zA-Z])     : Group 1 - End character of a value
	// \s+                   : Intermediate whitespace
	// (["{\[0-9tfn\-])      : Group 2 - Start character of the next value
	reMissingComma := regexp.MustCompile(`(["}\]0-9a-zA-Z])\s+(["{\[0-9tfn\-])`)

	// Perform replacement, insert comma
	// Note: This is aggressive, assuming all " following a value are start of keys.
	// In the extracted JSON fragment, this is usually safe.
	fixed := reMissingComma.ReplaceAllString(s, "$1,$2")

	return fixed
}
