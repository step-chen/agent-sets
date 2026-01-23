package types

// RawToolSchema represents raw schema info from an MCP tool
type RawToolSchema struct {
	Name        string
	InputSchema map[string]interface{}
}

// ChunkReviewConfig defines configuration for reviewing code chunks
type ChunkReviewConfig struct {
	ContextLines     int  `yaml:"context_lines"`
	FoldDeletesOver  int  `yaml:"fold_deletes_over"`
	RemoveWhitespace bool `yaml:"remove_whitespace"`
	CompressSpaces   bool `yaml:"compress_spaces"`
	RemoveBinaryDiff bool `yaml:"remove_binary_diff"`
}

// RawSchemaProvider defines interface for retrieving raw tool schemas from MCP
type RawSchemaProvider interface {
	GetRawToolSchemas() map[string][]RawToolSchema
}
