package bitbucket

import (
	"fmt"
	"log/slog"

	"pr-review-automation/internal/filter"
)

func init() {
	filter.Register("truncate", func(config map[string]interface{}) (filter.ResponseFilter, error) {
		maxLen := 100000 // Default value
		if val, ok := config["max_len"]; ok {
			if v, ok := val.(int); ok {
				maxLen = v
			} else if v, ok := val.(float64); ok {
				maxLen = int(v) // JSON unmarshal often produces floats
			} else {
				slog.Warn("invalid type for max_len in truncate filter config, using default", "type", fmt.Sprintf("%T", val))
			}
		}

		return NewResponseFilter(maxLen), nil
	})
}
