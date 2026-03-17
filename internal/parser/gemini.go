package parser

// GeminiResult holds normalized content extracted from a Gemini response.
// TODO: implement Gemini generateContent format parsing.
type GeminiResult struct {
	AssistantText []string
	ToolCalls     []ToolCall
	Model         string
}

// ParseGemini parses a Gemini generateContent response body.
// TODO: implement streaming and non-streaming Gemini response parsing.
func ParseGemini(_ []byte, _ bool) (GeminiResult, error) {
	// TODO: parse Gemini generateContent / streamGenerateContent format.
	// Non-streaming response shape:
	//   {"candidates":[{"content":{"parts":[{"text":"..."},{"functionCall":{"name":"...","args":{...}}}]}}],"modelVersion":"..."}
	// Streaming: newline-delimited JSON objects (not SSE).
	return GeminiResult{}, nil
}
