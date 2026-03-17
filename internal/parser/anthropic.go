package parser

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

// AnthropicResult holds the normalized content extracted from an Anthropic response.
type AnthropicResult struct {
	Thinking      []string
	AssistantText []string
	ToolCalls     []ToolCall
	Model         string
}

// ParseAnthropic dispatches to the streaming or non-streaming parser based on
// the isStream flag.
func ParseAnthropic(body []byte, isStream bool) (AnthropicResult, error) {
	if isStream {
		return parseAnthropicStream(body)
	}
	return parseAnthropicFull(body)
}

// --- Non-streaming ----------------------------------------------------------

type anthropicMessage struct {
	Model   string             `json:"model"`
	Content []anthropicContent `json:"content"`
}

type anthropicContent struct {
	Type     string          `json:"type"`
	Thinking string          `json:"thinking"`
	Text     string          `json:"text"`
	Name     string          `json:"name"`
	Input    json.RawMessage `json:"input"`
}

func parseAnthropicFull(body []byte) (AnthropicResult, error) {
	var msg anthropicMessage
	if err := json.Unmarshal(body, &msg); err != nil {
		return AnthropicResult{}, fmt.Errorf("anthropic full parse: %w", err)
	}

	result := AnthropicResult{Model: msg.Model}

	for _, block := range msg.Content {
		switch block.Type {
		case "thinking":
			if block.Thinking != "" {
				result.Thinking = append(result.Thinking, block.Thinking)
			}
		case "text":
			if block.Text != "" {
				result.AssistantText = append(result.AssistantText, block.Text)
			}
		case "tool_use":
			input := "{}"
			if len(block.Input) > 0 {
				input = string(block.Input)
			}
			result.ToolCalls = append(result.ToolCalls, ToolCall{
				Name:  block.Name,
				Input: input,
			})
		}
	}

	return result, nil
}

// --- Streaming (SSE) --------------------------------------------------------

// SSE event types we care about.
const (
	evMessageStart      = "message_start"
	evContentBlockStart = "content_block_start"
	evContentBlockDelta = "content_block_delta"
	evContentBlockStop  = "content_block_stop"
)

// Intermediate block state accumulated while replaying SSE deltas.
type streamBlock struct {
	blockType string // "thinking" | "text" | "tool_use"
	name      string // for tool_use
	buf       strings.Builder
}

type anthropicSSEEvent struct {
	Type string `json:"type"`

	// message_start
	Message *struct {
		Model string `json:"model"`
	} `json:"message,omitempty"`

	// content_block_start
	Index        int `json:"index"`
	ContentBlock *struct {
		Type string `json:"type"`
		Name string `json:"name"`
	} `json:"content_block,omitempty"`

	// content_block_delta
	Delta *struct {
		Type        string `json:"type"`
		Thinking    string `json:"thinking"`
		Text        string `json:"text"`
		PartialJSON string `json:"partial_json"`
	} `json:"delta,omitempty"`
}

func parseAnthropicStream(body []byte) (AnthropicResult, error) {
	var result AnthropicResult
	blocks := make(map[int]*streamBlock)

	scanner := bufio.NewScanner(bytes.NewReader(body))
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		data := strings.TrimPrefix(line, "data: ")
		if data == "" || data == "[DONE]" {
			continue
		}

		var ev anthropicSSEEvent
		if err := json.Unmarshal([]byte(data), &ev); err != nil {
			// Skip malformed events rather than aborting.
			continue
		}

		switch ev.Type {
		case evMessageStart:
			if ev.Message != nil {
				result.Model = ev.Message.Model
			}

		case evContentBlockStart:
			if ev.ContentBlock == nil {
				continue
			}
			blocks[ev.Index] = &streamBlock{
				blockType: ev.ContentBlock.Type,
				name:      ev.ContentBlock.Name,
			}

		case evContentBlockDelta:
			b, ok := blocks[ev.Index]
			if !ok || ev.Delta == nil {
				continue
			}
			switch ev.Delta.Type {
			case "thinking_delta":
				b.buf.WriteString(ev.Delta.Thinking)
			case "text_delta":
				b.buf.WriteString(ev.Delta.Text)
			case "input_json_delta":
				b.buf.WriteString(ev.Delta.PartialJSON)
			}

		case evContentBlockStop:
			b, ok := blocks[ev.Index]
			if !ok {
				continue
			}
			switch b.blockType {
			case "thinking":
				if s := b.buf.String(); s != "" {
					result.Thinking = append(result.Thinking, s)
				}
			case "text":
				if s := b.buf.String(); s != "" {
					result.AssistantText = append(result.AssistantText, s)
				}
			case "tool_use":
				input := b.buf.String()
				if input == "" {
					input = "{}"
				}
				result.ToolCalls = append(result.ToolCalls, ToolCall{
					Name:  b.name,
					Input: input,
				})
			}
			delete(blocks, ev.Index)
		}
	}

	return result, scanner.Err()
}
