package parser

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestParseAnthropicFull_TextOnly(t *testing.T) {
	body := mustJSON(map[string]any{
		"model": "claude-opus-4-6",
		"content": []any{
			map[string]any{"type": "text", "text": "Hello, world!"},
		},
	})

	result, err := ParseAnthropic(body, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Model != "claude-opus-4-6" {
		t.Errorf("model: got %q, want %q", result.Model, "claude-opus-4-6")
	}
	if len(result.AssistantText) != 1 || result.AssistantText[0] != "Hello, world!" {
		t.Errorf("assistant_text: got %v", result.AssistantText)
	}
	if len(result.Thinking) != 0 {
		t.Errorf("unexpected thinking blocks: %v", result.Thinking)
	}
	if len(result.ToolCalls) != 0 {
		t.Errorf("unexpected tool calls: %v", result.ToolCalls)
	}
}

func TestParseAnthropicFull_ThinkingAndText(t *testing.T) {
	body := mustJSON(map[string]any{
		"model": "claude-opus-4-6",
		"content": []any{
			map[string]any{"type": "thinking", "thinking": "Let me reason..."},
			map[string]any{"type": "text", "text": "The answer is 42."},
		},
	})

	result, err := ParseAnthropic(body, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(result.Thinking) != 1 || result.Thinking[0] != "Let me reason..." {
		t.Errorf("thinking: got %v", result.Thinking)
	}
	if len(result.AssistantText) != 1 || result.AssistantText[0] != "The answer is 42." {
		t.Errorf("assistant_text: got %v", result.AssistantText)
	}
}

func TestParseAnthropicFull_SingleToolCall(t *testing.T) {
	body := mustJSON(map[string]any{
		"model": "claude-opus-4-6",
		"content": []any{
			map[string]any{
				"type":  "tool_use",
				"name":  "Write",
				"input": map[string]any{"file_path": "main.go", "content": "package main"},
			},
		},
	})

	result, err := ParseAnthropic(body, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(result.ToolCalls) != 1 {
		t.Fatalf("tool_calls: got %d, want 1", len(result.ToolCalls))
	}
	if result.ToolCalls[0].Name != "Write" {
		t.Errorf("tool name: got %q, want %q", result.ToolCalls[0].Name, "Write")
	}
	// Input should be valid JSON containing file_path.
	var m map[string]any
	if err := json.Unmarshal([]byte(result.ToolCalls[0].Input), &m); err != nil {
		t.Errorf("tool input is not valid JSON: %v", err)
	}
	if m["file_path"] != "main.go" {
		t.Errorf("tool input file_path: got %v", m["file_path"])
	}
}

func TestParseAnthropicFull_MultipleToolCalls(t *testing.T) {
	body := mustJSON(map[string]any{
		"model": "claude-opus-4-6",
		"content": []any{
			map[string]any{
				"type":  "tool_use",
				"name":  "Read",
				"input": map[string]any{"file_path": "foo.go"},
			},
			map[string]any{
				"type":  "tool_use",
				"name":  "Write",
				"input": map[string]any{"file_path": "bar.go", "content": ""},
			},
		},
	})

	result, err := ParseAnthropic(body, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(result.ToolCalls) != 2 {
		t.Fatalf("tool_calls: got %d, want 2", len(result.ToolCalls))
	}
	if result.ToolCalls[0].Name != "Read" {
		t.Errorf("first tool: got %q", result.ToolCalls[0].Name)
	}
	if result.ToolCalls[1].Name != "Write" {
		t.Errorf("second tool: got %q", result.ToolCalls[1].Name)
	}
}

func TestParseAnthropicFull_EmptyContent(t *testing.T) {
	body := mustJSON(map[string]any{
		"model":   "claude-opus-4-6",
		"content": []any{},
	})

	result, err := ParseAnthropic(body, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(result.Thinking) != 0 || len(result.AssistantText) != 0 || len(result.ToolCalls) != 0 {
		t.Errorf("expected empty result, got %+v", result)
	}
}

func TestParseAnthropicFull_InvalidJSON(t *testing.T) {
	_, err := ParseAnthropic([]byte("not json"), false)
	if err == nil {
		t.Error("expected error for invalid JSON, got nil")
	}
}

// --- Streaming tests --------------------------------------------------------

func TestParseAnthropicStream_TextOnly(t *testing.T) {
	stream := buildAnthropicSSE([]sseEvent{
		{typ: "message_start", data: `{"type":"message_start","message":{"model":"claude-opus-4-6"}}`},
		{typ: "content_block_start", data: `{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`},
		{typ: "content_block_delta", data: `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hello"}}`},
		{typ: "content_block_delta", data: `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":" world"}}`},
		{typ: "content_block_stop", data: `{"type":"content_block_stop","index":0}`},
		{typ: "message_stop", data: `{"type":"message_stop"}`},
	})

	result, err := ParseAnthropic([]byte(stream), true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Model != "claude-opus-4-6" {
		t.Errorf("model: got %q", result.Model)
	}
	if len(result.AssistantText) != 1 || result.AssistantText[0] != "Hello world" {
		t.Errorf("assistant_text: got %v", result.AssistantText)
	}
}

func TestParseAnthropicStream_ThinkingAndToolCall(t *testing.T) {
	stream := buildAnthropicSSE([]sseEvent{
		{typ: "message_start", data: `{"type":"message_start","message":{"model":"claude-opus-4-6"}}`},
		{typ: "content_block_start", data: `{"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":""}}`},
		{typ: "content_block_delta", data: `{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"I should use Write"}}`},
		{typ: "content_block_stop", data: `{"type":"content_block_stop","index":0}`},
		{typ: "content_block_start", data: `{"type":"content_block_start","index":1,"content_block":{"type":"tool_use","name":"Write","id":"toolu_01"}}`},
		{typ: "content_block_delta", data: `{"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"{\"file_path\":"}}`},
		{typ: "content_block_delta", data: `{"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"\"hello.go\"}"}}`},
		{typ: "content_block_stop", data: `{"type":"content_block_stop","index":1}`},
		{typ: "message_stop", data: `{"type":"message_stop"}`},
	})

	result, err := ParseAnthropic([]byte(stream), true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(result.Thinking) != 1 || result.Thinking[0] != "I should use Write" {
		t.Errorf("thinking: got %v", result.Thinking)
	}
	if len(result.ToolCalls) != 1 {
		t.Fatalf("tool_calls: got %d, want 1", len(result.ToolCalls))
	}
	if result.ToolCalls[0].Name != "Write" {
		t.Errorf("tool name: got %q", result.ToolCalls[0].Name)
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(result.ToolCalls[0].Input), &m); err != nil {
		t.Errorf("tool input not valid JSON: %v — raw: %s", err, result.ToolCalls[0].Input)
	}
}

func TestParseAnthropicStream_EmptyStream(t *testing.T) {
	result, err := ParseAnthropic([]byte(""), true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Thinking) != 0 || len(result.AssistantText) != 0 || len(result.ToolCalls) != 0 {
		t.Errorf("expected empty result, got %+v", result)
	}
}

// --- helpers ----------------------------------------------------------------

type sseEvent struct {
	typ  string
	data string
}

func buildAnthropicSSE(events []sseEvent) string {
	var sb strings.Builder
	for _, e := range events {
		sb.WriteString("event: ")
		sb.WriteString(e.typ)
		sb.WriteString("\ndata: ")
		sb.WriteString(e.data)
		sb.WriteString("\n\n")
	}
	return sb.String()
}

func mustJSON(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}
