package parser

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestParseOpenAIFull_TextOnly(t *testing.T) {
	body := mustJSON(map[string]any{
		"model": "gpt-4o",
		"choices": []any{
			map[string]any{
				"message": map[string]any{
					"content": "Hello from GPT",
				},
			},
		},
	})

	result, err := ParseOpenAI(body, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Model != "gpt-4o" {
		t.Errorf("model: got %q, want %q", result.Model, "gpt-4o")
	}
	if len(result.AssistantText) != 1 || result.AssistantText[0] != "Hello from GPT" {
		t.Errorf("assistant_text: got %v", result.AssistantText)
	}
	if len(result.ToolCalls) != 0 {
		t.Errorf("unexpected tool calls: %v", result.ToolCalls)
	}
}

func TestParseOpenAIFull_SingleToolCall(t *testing.T) {
	body := mustJSON(map[string]any{
		"model": "gpt-4o",
		"choices": []any{
			map[string]any{
				"message": map[string]any{
					"content": nil,
					"tool_calls": []any{
						map[string]any{
							"function": map[string]any{
								"name":      "write_file",
								"arguments": `{"path":"main.go","content":"package main"}`,
							},
						},
					},
				},
			},
		},
	})

	result, err := ParseOpenAI(body, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(result.ToolCalls) != 1 {
		t.Fatalf("tool_calls: got %d, want 1", len(result.ToolCalls))
	}
	if result.ToolCalls[0].Name != "write_file" {
		t.Errorf("tool name: got %q", result.ToolCalls[0].Name)
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(result.ToolCalls[0].Input), &m); err != nil {
		t.Errorf("tool input not valid JSON: %v", err)
	}
	if m["path"] != "main.go" {
		t.Errorf("tool input path: got %v", m["path"])
	}
}

func TestParseOpenAIFull_MultipleToolCalls(t *testing.T) {
	body := mustJSON(map[string]any{
		"model": "gpt-4o",
		"choices": []any{
			map[string]any{
				"message": map[string]any{
					"tool_calls": []any{
						map[string]any{
							"function": map[string]any{"name": "read_file", "arguments": `{"path":"a.go"}`},
						},
						map[string]any{
							"function": map[string]any{"name": "write_file", "arguments": `{"path":"b.go"}`},
						},
					},
				},
			},
		},
	})

	result, err := ParseOpenAI(body, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(result.ToolCalls) != 2 {
		t.Fatalf("tool_calls: got %d, want 2", len(result.ToolCalls))
	}
	if result.ToolCalls[0].Name != "read_file" {
		t.Errorf("first tool: got %q", result.ToolCalls[0].Name)
	}
	if result.ToolCalls[1].Name != "write_file" {
		t.Errorf("second tool: got %q", result.ToolCalls[1].Name)
	}
}

func TestParseOpenAIFull_EmptyChoices(t *testing.T) {
	body := mustJSON(map[string]any{
		"model":   "gpt-4o",
		"choices": []any{},
	})

	result, err := ParseOpenAI(body, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.AssistantText) != 0 || len(result.ToolCalls) != 0 {
		t.Errorf("expected empty result, got %+v", result)
	}
}

func TestParseOpenAIFull_InvalidJSON(t *testing.T) {
	_, err := ParseOpenAI([]byte("{bad json"), false)
	if err == nil {
		t.Error("expected error for invalid JSON, got nil")
	}
}

// --- Streaming tests --------------------------------------------------------

func TestParseOpenAIStream_TextOnly(t *testing.T) {
	stream := buildOpenAISSE([]string{
		`{"model":"gpt-4o","choices":[{"delta":{"role":"assistant","content":"Hello"}}]}`,
		`{"choices":[{"delta":{"content":" world"}}]}`,
		`{"choices":[{"delta":{},"finish_reason":"stop"}]}`,
	})

	result, err := ParseOpenAI([]byte(stream), true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Model != "gpt-4o" {
		t.Errorf("model: got %q", result.Model)
	}
	if len(result.AssistantText) != 1 || result.AssistantText[0] != "Hello world" {
		t.Errorf("assistant_text: got %v", result.AssistantText)
	}
}

func TestParseOpenAIStream_ToolCall(t *testing.T) {
	stream := buildOpenAISSE([]string{
		`{"model":"gpt-4o","choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"write_file","arguments":""}}]}}]}`,
		`{"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"path\":"}}]}}]}`,
		`{"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\"main.go\"}"}}]}}]}`,
		`{"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
	})

	result, err := ParseOpenAI([]byte(stream), true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(result.ToolCalls) != 1 {
		t.Fatalf("tool_calls: got %d, want 1", len(result.ToolCalls))
	}
	if result.ToolCalls[0].Name != "write_file" {
		t.Errorf("tool name: got %q", result.ToolCalls[0].Name)
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(result.ToolCalls[0].Input), &m); err != nil {
		t.Errorf("tool input not valid JSON: %v — raw: %s", err, result.ToolCalls[0].Input)
	}
}

func TestParseOpenAIStream_Empty(t *testing.T) {
	result, err := ParseOpenAI([]byte("data: [DONE]\n"), true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.AssistantText) != 0 || len(result.ToolCalls) != 0 {
		t.Errorf("expected empty result, got %+v", result)
	}
}

// --- helpers ----------------------------------------------------------------

func buildOpenAISSE(chunks []string) string {
	var sb strings.Builder
	for _, c := range chunks {
		sb.WriteString("data: ")
		sb.WriteString(c)
		sb.WriteString("\n\n")
	}
	sb.WriteString("data: [DONE]\n")
	return sb.String()
}
