package pipeline

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"pr-review-automation/internal/config"
	"pr-review-automation/internal/types"
	"strings"
	"text/template"
)

// PromptLoader loads prompts from filesystem
type PromptLoader struct {
	baseDir           string
	rawSchemaProvider types.RawSchemaProvider
}

// NewPromptLoader creates a new prompt loader
func NewPromptLoader(baseDir string) *PromptLoader {
	return &PromptLoader{baseDir: baseDir}
}

// SetRawSchemaProvider sets the raw schema provider for dynamic prompt generation
func (l *PromptLoader) SetRawSchemaProvider(p types.RawSchemaProvider) {
	l.rawSchemaProvider = p
}

// Load returns prompt content with fallback hierarchy
func (l *PromptLoader) Load(project, language string, extraData map[string]interface{}) (string, error) {
	candidates := []string{
		filepath.Join(l.baseDir, project, language+".md"),
		filepath.Join(l.baseDir, project, "default.md"),
		filepath.Join(l.baseDir, "default", language+".md"),
		filepath.Join(l.baseDir, "default", "default.md"),
	}

	for _, path := range candidates {
		data, err := os.ReadFile(path)
		if err == nil {
			return l.render(string(data), extraData)
		}
		if !os.IsNotExist(err) {
			return "", fmt.Errorf("read prompt %s: %w", path, err)
		}
	}

	return "", fmt.Errorf("no prompt found for project=%q language=%q", project, language)
}

// PromptData contains data available to prompt templates
type PromptData struct {
	ToolBitbucketGetDiff        string
	ToolBitbucketGetComments    string
	ToolBitbucketAddComment     string
	ToolBitbucketGetChanges     string
	ToolBitbucketGetFileContent string
	ToolBitbucketGetPullRequest string
	ProjectKey                  string
	RepoSlug                    string
}

// NewPromptData creates a new PromptData with values from config
func NewPromptData() PromptData {
	return PromptData{
		ToolBitbucketGetDiff:        config.ToolBitbucketGetDiff,
		ToolBitbucketGetComments:    config.ToolBitbucketGetComments,
		ToolBitbucketAddComment:     config.ToolBitbucketAddComment,
		ToolBitbucketGetChanges:     config.ToolBitbucketGetChanges,
		ToolBitbucketGetFileContent: config.ToolBitbucketGetFileContent,
		ToolBitbucketGetPullRequest: config.ToolBitbucketGetPullRequest,
	}
}

func (l *PromptLoader) render(tmplContent string, extraData map[string]interface{}) (string, error) {
	data := NewPromptData()
	if val, ok := extraData["ProjectKey"].(string); ok {
		data.ProjectKey = val
	}
	if val, ok := extraData["RepoSlug"].(string); ok {
		data.RepoSlug = val
	}
	l.enrichFromRawSchema(&data)

	// Merge PromptData fields and extraData into a single map
	// This ensures both the auto-enriched tool signatures AND dynamic pipeline data are available
	mergedData := make(map[string]interface{})

	// 1. Add default fields from PromptData
	mergedData["ToolBitbucketGetDiff"] = data.ToolBitbucketGetDiff
	mergedData["ToolBitbucketGetComments"] = data.ToolBitbucketGetComments
	mergedData["ToolBitbucketAddComment"] = data.ToolBitbucketAddComment
	mergedData["ToolBitbucketGetChanges"] = data.ToolBitbucketGetChanges
	mergedData["ToolBitbucketGetFileContent"] = data.ToolBitbucketGetFileContent
	mergedData["ToolBitbucketGetPullRequest"] = data.ToolBitbucketGetPullRequest
	mergedData["ProjectKey"] = data.ProjectKey
	mergedData["RepoSlug"] = data.RepoSlug

	// 2. Add everything from extraData (overwriting defaults if necessary)
	for k, v := range extraData {
		mergedData[k] = v
	}

	tmpl, err := template.New("prompt").Parse(tmplContent)
	if err != nil {
		return "", fmt.Errorf("parse prompt template: %w", err)
	}
	var sb strings.Builder
	if err := tmpl.Execute(&sb, mergedData); err != nil {
		return "", fmt.Errorf("execute prompt template: %w", err)
	}
	return sb.String(), nil
}

func (l *PromptLoader) enrichFromRawSchema(data *PromptData) {
	if l.rawSchemaProvider == nil {
		return
	}
	schemas := l.rawSchemaProvider.GetRawToolSchemas()
	flattenedSchemas := make(map[string]map[string]interface{})
	for _, toolList := range schemas {
		for _, t := range toolList {
			flattenedSchemas[t.Name] = t.InputSchema
		}
	}
	slog.Debug("enriching prompt data (raw MCP)", "tools_found", len(flattenedSchemas))
	if sig := getRawToolSignature(flattenedSchemas, data.ToolBitbucketGetDiff); sig != "" {
		data.ToolBitbucketGetDiff = sig
	}
	if sig := getRawToolSignature(flattenedSchemas, data.ToolBitbucketGetComments); sig != "" {
		data.ToolBitbucketGetComments = sig
	}
	if sig := getRawToolSignature(flattenedSchemas, data.ToolBitbucketAddComment); sig != "" {
		data.ToolBitbucketAddComment = sig
	}
	if sig := getRawToolSignature(flattenedSchemas, data.ToolBitbucketGetChanges); sig != "" {
		data.ToolBitbucketGetChanges = sig
	}
	if sig := getRawToolSignature(flattenedSchemas, data.ToolBitbucketGetFileContent); sig != "" {
		data.ToolBitbucketGetFileContent = sig
	}
	if sig := getRawToolSignature(flattenedSchemas, data.ToolBitbucketGetPullRequest); sig != "" {
		data.ToolBitbucketGetPullRequest = sig
	}
}

func getRawToolSignature(schemas map[string]map[string]interface{}, toolName string) string {
	schema, ok := schemas[toolName]
	if !ok || schema == nil {
		return ""
	}
	var params []string
	if props, ok := schema["properties"].(map[string]interface{}); ok {
		for name, propSchema := range props {
			var typeName string
			if propMap, ok := propSchema.(map[string]interface{}); ok {
				if t, ok := propMap["type"].(string); ok {
					typeName = t
				}
			}
			if typeName != "" {
				params = append(params, fmt.Sprintf("%s: %s", name, typeName))
			} else {
				params = append(params, name)
			}
		}
	}
	return fmt.Sprintf("%s(%s)", toolName, strings.Join(params, ", "))
}

// LoadPrompt loads and renders a specific prompt file directly from the base directory.
// Name should be relative path without extension, e.g. "pipeline/stage1"
func (l *PromptLoader) LoadPrompt(name string, data map[string]interface{}) (string, error) {
	// If name has extension, remove it
	name = strings.TrimSuffix(name, ".md")

	path := filepath.Join(l.baseDir, name+".md")
	tmplData, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read prompt %s: %w", path, err)
	}

	return l.render(string(tmplData), data)
}
