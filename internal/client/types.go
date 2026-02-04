package client

// RawToolSchema represents raw schema info from an MCP tool
type RawToolSchema struct {
	Name        string
	InputSchema map[string]interface{}
}

// RawSchemaProvider defines interface for retrieving raw tool schemas from MCP
type RawSchemaProvider interface {
	GetRawToolSchemas() map[string][]RawToolSchema
}
