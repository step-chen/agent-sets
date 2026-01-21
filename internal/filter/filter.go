package filter

// PayloadFilter defines the interface for filtering webhook payloads
type PayloadFilter interface {
	// Filter receives raw JSON bytes and returns filtered JSON bytes
	Filter(payload []byte) []byte
}

// ResponseFilter defines the interface for filtering MCP tool responses
type ResponseFilter interface {
	// Filter receives tool name and raw response, returning filtered response
	Filter(toolName string, response any) any
}
