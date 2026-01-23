package agent

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"text/template"

	"pr-review-automation/internal/config"
	"pr-review-automation/internal/types"

	"google.golang.org/genai"
)

// SchemaProvider defines interface for retrieving tool schemas (ADK based)
type SchemaProvider interface {
	GetToolDeclarations() map[string][]*genai.FunctionDeclaration
}

// PromptLoader loads prompts from filesystem with fallback hierarchy
type PromptLoader struct {
	baseDir           string
	schemaProvider    SchemaProvider          // Deprecated: ADK based, Parameters is nil
	rawSchemaProvider types.RawSchemaProvider // New: raw MCP InputSchema
}

// NewPromptLoader creates a new prompt loader with the given base directory
func NewPromptLoader(baseDir string) *PromptLoader {
	return &PromptLoader{baseDir: baseDir}
}

// SetSchemaProvider sets the schema provider for dynamic prompt generation (deprecated)
func (l *PromptLoader) SetSchemaProvider(p SchemaProvider) {
	l.schemaProvider = p
}

// SetRawSchemaProvider sets the raw schema provider for dynamic prompt generation
func (l *PromptLoader) SetRawSchemaProvider(p types.RawSchemaProvider) {
	l.rawSchemaProvider = p
}

// Load returns prompt content with fallback hierarchy:
// 1. {baseDir}/{project}/{language}.md
// 2. {baseDir}/{project}/default.md
// 3. {baseDir}/default/{language}.md
// 4. {baseDir}/default/default.md
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

	return "", fmt.Errorf("no prompt found for project=%q language=%q, tried: %v", project, language, candidates)
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

// render processes the prompt template with PromptData
func (l *PromptLoader) render(tmplContent string, extraData map[string]interface{}) (string, error) {
	// 1. Create Data
	data := NewPromptData()

	// Inject extra data (ProjectKey, RepoSlug)
	if val, ok := extraData["ProjectKey"].(string); ok {
		data.ProjectKey = val
	}
	if val, ok := extraData["RepoSlug"].(string); ok {
		data.RepoSlug = val
	}

	// Enrich with schemas if available
	l.enrichPromptData(&data)

	// 2. Parse Template
	tmpl, err := template.New("prompt").Parse(tmplContent)
	if err != nil {
		return "", fmt.Errorf("parse prompt template: %w", err)
	}

	// 3. Execute
	var sb strings.Builder
	if err := tmpl.Execute(&sb, data); err != nil {
		return "", fmt.Errorf("execute prompt template: %w", err)
	}

	return sb.String(), nil
}

// enrichPromptData updates PromptData fields with tool signatures if schema is available
func (l *PromptLoader) enrichPromptData(data *PromptData) {
	// Prefer raw schema provider (direct MCP) over ADK-based one
	if l.rawSchemaProvider != nil {
		l.enrichFromRawSchema(data)
		return
	}

	// Fallback to ADK-based provider (usually Parameters is nil)
	if l.schemaProvider == nil {
		return
	}

	toolDecls := l.schemaProvider.GetToolDeclarations()
	flattenedTools := make(map[string]*genai.FunctionDeclaration)
	for _, decls := range toolDecls {
		for _, decl := range decls {
			flattenedTools[decl.Name] = decl
		}
	}

	slog.Debug("enriching prompt data (ADK)", "tools_found", len(flattenedTools))

	// Update fields
	// Note: We are manually updating known fields here to avoid complex reflection for now,
	// but reflection could be used if the struct grows significantly.
	// Given PromptData is simple, explicit assignment is clearer.
	if sig := getToolSignature(flattenedTools, data.ToolBitbucketGetDiff); sig != "" {
		data.ToolBitbucketGetDiff = sig
	}
	if sig := getToolSignature(flattenedTools, data.ToolBitbucketGetComments); sig != "" {
		data.ToolBitbucketGetComments = sig
	}
	if sig := getToolSignature(flattenedTools, data.ToolBitbucketAddComment); sig != "" {
		data.ToolBitbucketAddComment = sig
	}
	if sig := getToolSignature(flattenedTools, data.ToolBitbucketGetChanges); sig != "" {
		data.ToolBitbucketGetChanges = sig
	}
	if sig := getToolSignature(flattenedTools, data.ToolBitbucketGetFileContent); sig != "" {
		data.ToolBitbucketGetFileContent = sig
	}
	if sig := getToolSignature(flattenedTools, data.ToolBitbucketGetPullRequest); sig != "" {
		data.ToolBitbucketGetPullRequest = sig
	}
}

// enrichFromRawSchema uses raw MCP InputSchema to build tool signatures
func (l *PromptLoader) enrichFromRawSchema(data *PromptData) {
	schemas := l.rawSchemaProvider.GetRawToolSchemas()

	// Flatten schemas
	flattenedSchemas := make(map[string]map[string]interface{})
	for _, toolList := range schemas {
		for _, t := range toolList {
			flattenedSchemas[t.Name] = t.InputSchema
		}
	}

	slog.Debug("enriching prompt data (raw MCP)", "tools_found", len(flattenedSchemas))

	// Update fields
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
			// Extract type if available
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

	sig := fmt.Sprintf("%s(%s)", toolName, strings.Join(params, ", "))
	slog.Debug("injected tool signature (raw)", "tool", toolName, "signature", sig)
	return sig
}

func getToolSignature(tools map[string]*genai.FunctionDeclaration, toolName string) string {
	decl, ok := tools[toolName]
	if !ok {
		// Log missing tool to help debugging
		slog.Debug("tool signature not found", "tool_name", toolName)
		return ""
	}
	sig := formatToolSignature(decl)
	slog.Debug("injected tool signature", "tool", toolName, "signature", sig)
	return sig
}

func formatToolSignature(decl *genai.FunctionDeclaration) string {
	var params []string
	if decl.Parameters != nil {
		// Use JSON round-trip to access properties safely as we don't know the exact struct fields
		// and it matches the pattern in openai_adapter.go
		b, err := json.Marshal(decl.Parameters)
		if err == nil {
			slog.Debug("formatToolSignature: raw schema", "tool", decl.Name, "json", string(b))
			var schema map[string]interface{}
			if err := json.Unmarshal(b, &schema); err == nil {
				if props, ok := schema["properties"].(map[string]interface{}); ok {
					for name := range props {
						params = append(params, name)
					}
				} else {
					slog.Debug("formatToolSignature: no properties found", "tool", decl.Name, "schema_keys", func() []string {
						keys := make([]string, 0, len(schema))
						for k := range schema {
							keys = append(keys, k)
						}
						return keys
					}())
				}
			}
		}
	} else {
		slog.Debug("formatToolSignature: Parameters is nil", "tool", decl.Name)
	}
	return fmt.Sprintf("%s(%s)", decl.Name, strings.Join(params, ", "))
}

// LoadPrompt loads a specific prompt file directly from the base directory.
// Name should be relative path without extension, e.g. "system/webhook_parser"
func (l *PromptLoader) LoadPrompt(name string) (string, error) {
	path := filepath.Join(l.baseDir, name+".md")
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read prompt %s: %w", path, err)
	}
	return string(data), nil
}

// languageExtensions maps file extensions to language identifiers
var languageExtensions = map[string]string{
	".cpp": "cpp", ".cc": "cpp", ".cxx": "cpp", ".c": "cpp", ".h": "cpp", ".hpp": "cpp", ".hxx": "cpp",
	".go":   "golang",
	".py":   "python",
	".java": "java",
	".ts":   "typescript", ".tsx": "typescript",
	".js": "javascript", ".jsx": "javascript",
	".rs": "rust",
	".kt": "kotlin", ".kts": "kotlin",
	".swift": "swift",
	".rb":    "ruby",
	".cs":    "csharp",
}

// DetectLanguage detects the primary language from a list of file paths
// based on file extensions. Returns "default" if no language is detected.
func DetectLanguage(files []string) string {
	counts := make(map[string]int)

	for _, file := range files {
		ext := strings.ToLower(filepath.Ext(file))
		if lang, ok := languageExtensions[ext]; ok {
			counts[lang]++
		}
	}

	if len(counts) == 0 {
		return "default"
	}

	// Find the language with the most files
	maxLang := "default"
	maxCount := 0
	for lang, count := range counts {
		if count > maxCount {
			maxCount = count
			maxLang = lang
		}
	}

	return maxLang
}
