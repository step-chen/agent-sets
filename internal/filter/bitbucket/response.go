package bitbucket

import (
	"encoding/json"
	"log/slog"
	"pr-review-automation/internal/config"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// ResponseFilter filters Bitbucket MCP tool responses
type ResponseFilter struct {
	MaxStringLen int
}

// NewResponseFilter creates a new Bitbucket ResponseFilter
func NewResponseFilter(maxStringLen int) *ResponseFilter {
	return &ResponseFilter{
		MaxStringLen: maxStringLen,
	}
}

// Filter filters the response based on the tool name
func (f *ResponseFilter) Filter(toolName string, response any) any {
	jsonBytes, err := json.Marshal(response)
	if err != nil {
		return response
	}

	var filteredBytes []byte

	switch toolName {
	case config.ToolBitbucketGetComments:
		filteredBytes = f.filterComments(jsonBytes)
	case config.ToolBitbucketGetPullRequest:
		filteredBytes = f.filterPullRequest(jsonBytes)
	case config.ToolBitbucketGetChanges:
		filteredBytes = f.filterChanges(jsonBytes)
	case config.ToolBitbucketGetFileContent:
		filteredBytes = f.filterLongStrings(jsonBytes, f.MaxStringLen)
	default:
		// Generic long string filter for any other unknown tool (including GetDiff)
		filteredBytes = f.filterLongStrings(jsonBytes, f.MaxStringLen)
	}

	var filtered any
	if err := json.Unmarshal(filteredBytes, &filtered); err != nil {
		return response
	}
	return filtered
}

func (f *ResponseFilter) filterComments(data []byte) []byte {
	result := string(data)

	// Iterate over values array to prune each comment
	gjson.GetBytes(data, "values").ForEach(func(idx, val gjson.Result) bool {
		prefix := "values." + idx.String()

		// Simplify Author
		result, _ = sjson.Delete(result, prefix+".author.id")
		result, _ = sjson.Delete(result, prefix+".author.emailAddress")
		result, _ = sjson.Delete(result, prefix+".author.slug")
		result, _ = sjson.Delete(result, prefix+".author.type")
		result, _ = sjson.Delete(result, prefix+".author.active")
		result, _ = sjson.Delete(result, prefix+".author.links")

		// Remove metadata
		result, _ = sjson.Delete(result, prefix+".links")
		result, _ = sjson.Delete(result, prefix+".permittedOperations")
		result, _ = sjson.Delete(result, prefix+".version")
		result, _ = sjson.Delete(result, prefix+".state")
		result, _ = sjson.Delete(result, prefix+".createdDate")
		result, _ = sjson.Delete(result, prefix+".updatedDate")
		result, _ = sjson.Delete(result, prefix+".severity")

		// Simplify Content
		result, _ = sjson.Delete(result, prefix+".content.markup")
		result, _ = sjson.Delete(result, prefix+".content.html")

		// Truncate text (reduced from 1000 to 500 - dedup only needs first 50 chars)
		text := gjson.Get(result, prefix+".text").String()
		if len(text) > config.MaxCommentLength {
			result, _ = sjson.Set(result, prefix+".text", text[:config.MaxCommentLength]+config.TruncatedSuffix)
		}

		return true
	})

	return []byte(result)
}

func (f *ResponseFilter) filterPullRequest(data []byte) []byte {
	result := string(data)

	// Remove top level metadata
	result, _ = sjson.Delete(result, "links")
	result, _ = sjson.Delete(result, "reviewers")
	result, _ = sjson.Delete(result, "participants")
	result, _ = sjson.Delete(result, "version")
	result, _ = sjson.Delete(result, "createdDate")
	result, _ = sjson.Delete(result, "updatedDate")
	result, _ = sjson.Delete(result, "closed")
	result, _ = sjson.Delete(result, "locked")

	// Simplify author
	result, _ = sjson.Delete(result, "author.user.id")
	result, _ = sjson.Delete(result, "author.user.emailAddress")
	result, _ = sjson.Delete(result, "author.user.slug")
	result, _ = sjson.Delete(result, "author.user.type")
	result, _ = sjson.Delete(result, "author.user.active")
	result, _ = sjson.Delete(result, "author.user.links")
	result, _ = sjson.Delete(result, "author.role")
	result, _ = sjson.Delete(result, "author.approved")
	result, _ = sjson.Delete(result, "author.status")

	return []byte(result)
}

func (f *ResponseFilter) filterChanges(data []byte) []byte {
	result := string(data)

	gjson.GetBytes(data, "values").ForEach(func(idx, val gjson.Result) bool {
		prefix := "values." + idx.String()
		result, _ = sjson.Delete(result, prefix+".links")
		result, _ = sjson.Delete(result, prefix+".contentId")
		result, _ = sjson.Delete(result, prefix+".fromContentId")
		result, _ = sjson.Delete(result, prefix+".properties")

		// Prune path details
		result, _ = sjson.Delete(result, prefix+".path.components")
		result, _ = sjson.Delete(result, prefix+".path.parent")
		result, _ = sjson.Delete(result, prefix+".executable")
		result, _ = sjson.Delete(result, prefix+".percentUnchanged")

		return true
	})

	return []byte(result)
}

func (f *ResponseFilter) filterLongStrings(data []byte, maxLen int) []byte {
	var m interface{}
	if err := json.Unmarshal(data, &m); err != nil {
		return data
	}

	f.truncateRecursive(&m, maxLen)

	newData, err := json.Marshal(m)
	if err != nil {
		return data
	}
	return newData
}

func (f *ResponseFilter) truncateRecursive(val *interface{}, maxLen int) {
	if val == nil || *val == nil {
		return
	}

	switch v := (*val).(type) {
	case string:
		if len(v) > maxLen {
			slog.Info("truncating long response string", "original_len", len(v), "limit", maxLen)
			(*val) = v[:maxLen] + "... [TRUNCATED]"
		}
	case map[string]interface{}:
		for k, child := range v {
			f.truncateRecursive(&child, maxLen)
			v[k] = child
		}
	case []interface{}:
		for i, child := range v {
			f.truncateRecursive(&child, maxLen)
			v[i] = child
		}
	}
}
