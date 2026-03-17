package parser

// ToolCall holds the name and JSON-encoded input for a single tool invocation.
type ToolCall struct {
	Name  string
	Input string // JSON object as a string
}
