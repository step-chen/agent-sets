package pipeline

import (
	"encoding/json"
	"sync"

	"pr-review-automation/internal/domain"

	"github.com/invopop/jsonschema"
)

// SchemaProvider handles JSON Schema generation from Go structs
type SchemaProvider struct {
	mu           sync.Mutex
	cachedSchema map[string]interface{}
}

// NewSchemaProvider creates a new SchemaProvider
func NewSchemaProvider() *SchemaProvider {
	return &SchemaProvider{}
}

// GetSchema returns the JSON Schema for ReviewResult as a map
func (p *SchemaProvider) GetSchema() map[string]interface{} {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.cachedSchema != nil {
		return p.cachedSchema
	}

	reflector := jsonschema.Reflector{
		RequiredFromJSONSchemaTags: true, // Respect jsonschema:"required" tags
		ExpandedStruct:             true, // Flatten embedded structs (if any)
	}

	// Generate schema for ReviewResult
	schema := reflector.Reflect(&domain.ReviewResult{})

	// Convert to map for flexible usage
	data, _ := json.Marshal(schema)
	var schemaMap map[string]interface{}
	_ = json.Unmarshal(data, &schemaMap)

	p.cachedSchema = schemaMap
	return p.cachedSchema
}

// GetSchemaJSON returns the JSON Schema as a formatted string for prompt injection
func (p *SchemaProvider) GetSchemaJSON() string {
	schema := p.GetSchema()
	data, _ := json.MarshalIndent(schema, "", "  ")
	return string(data)
}
