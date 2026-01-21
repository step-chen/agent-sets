package bitbucket

import (
	"encoding/json"
)

// PayloadFilter implements filtering for Bitbucket Webhook payloads
type PayloadFilter struct{}

// NewPayloadFilter creates a new Bitbucket PayloadFilter
func NewPayloadFilter() *PayloadFilter {
	return &PayloadFilter{}
}

// Filter filters the raw payload bytes
func (f *PayloadFilter) Filter(payload []byte) []byte {
	var data map[string]interface{}
	if err := json.Unmarshal(payload, &data); err != nil {
		return payload
	}

	prune(data, 0)

	result, err := json.Marshal(data)
	if err != nil {
		return payload
	}
	return result
}

func prune(v interface{}, depth int) {
	if depth > 10 {
		return
	}

	switch val := v.(type) {
	case map[string]interface{}:
		for k, v2 := range val {
			// Prune based on keys
			if ShouldPrune(k) {
				delete(val, k)
				continue
			}
			// Recursive prune
			prune(v2, depth+1)
		}

		// Specialized simplifications for common objects
		if isUserObject(val) {
			simplifyUser(val)
		} else if isRepositoryObject(val) {
			simplifyRepository(val)
		} else if isRefObject(val) {
			simplifyRef(val)
		}

	case []interface{}:
		for _, item := range val {
			prune(item, depth+1)
		}
	}
}

func isUserObject(m map[string]interface{}) bool {
	// Heuristic: has displayName and slug and id
	_, hasDisplayName := m["displayName"]
	_, hasSlug := m["slug"]
	_, hasID := m["id"]
	return hasDisplayName && hasSlug && hasID
}

func simplifyUser(m map[string]interface{}) {
	// Only keep displayName to save huge tokens
	displayName, _ := m["displayName"]
	// clear other fields
	for k := range m {
		delete(m, k)
	}
	if displayName != nil {
		m["displayName"] = displayName
	}
}

func isRepositoryObject(m map[string]interface{}) bool {
	// Heuristic: has slug and project
	_, hasSlug := m["slug"]
	_, hasProject := m["project"]
	return hasSlug && hasProject
}

func simplifyRepository(m map[string]interface{}) {
	slug := m["slug"]
	var projectKey interface{}
	if project, ok := m["project"].(map[string]interface{}); ok {
		projectKey = project["key"]
	}

	for k := range m {
		delete(m, k)
	}
	if slug != nil {
		m["slug"] = slug
	}
	if projectKey != nil {
		m["project"] = map[string]interface{}{"key": projectKey}
	}
}

func isRefObject(m map[string]interface{}) bool {
	_, hasDisplayID := m["displayId"]
	_, hasID := m["id"]
	return hasDisplayID && hasID
}

func simplifyRef(m map[string]interface{}) {
	// keep displayId, id, and repository (which is already simplified)
	displayId := m["displayId"]
	id := m["id"]
	repo := m["repository"]

	for k := range m {
		delete(m, k)
	}
	if displayId != nil {
		m["displayId"] = displayId
	}
	if id != nil {
		m["id"] = id
	}
	if repo != nil {
		m["repository"] = repo
	}
}
