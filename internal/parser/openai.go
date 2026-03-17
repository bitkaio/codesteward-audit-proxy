package parser

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

// OpenAIResult holds the normalized content extracted from an OpenAI response.
type OpenAIResult struct {
	AssistantText []string
	ToolCalls     []ToolCall
	Model         string
}

// ParseOpenAI dispatches to the streaming or non-streaming parser.
func ParseOpenAI(body []byte, isStream bool) (OpenAIResult, error) {
	if isStream {
		return parseOpenAIStream(body)
	}
	return parseOpenAIFull(body)
}

// --- Non-streaming ----------------------------------------------------------

type openAIResponse struct {
	Model   string `json:"model"`
	Choices []struct {
		Message struct {
			Content   string              `json:"content"`
			ToolCalls []openAIToolCallObj `json:"tool_calls"`
		} `json:"message"`
	} `json:"choices"`
}

type openAIToolCallObj struct {
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

func parseOpenAIFull(body []byte) (OpenAIResult, error) {
	var resp openAIResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return OpenAIResult{}, fmt.Errorf("openai full parse: %w", err)
	}

	result := OpenAIResult{Model: resp.Model}

	for _, choice := range resp.Choices {
		if choice.Message.Content != "" {
			result.AssistantText = append(result.AssistantText, choice.Message.Content)
		}
		for _, tc := range choice.Message.ToolCalls {
			input := tc.Function.Arguments
			if input == "" {
				input = "{}"
			}
			result.ToolCalls = append(result.ToolCalls, ToolCall{
				Name:  tc.Function.Name,
				Input: input,
			})
		}
	}

	return result, nil
}

// --- Streaming (SSE) --------------------------------------------------------

type openAIChunk struct {
	Model   string `json:"model"`
	Choices []struct {
		Delta struct {
			Content   string `json:"content"`
			ToolCalls []struct {
				Index    int `json:"index"`
				Function struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				} `json:"function"`
			} `json:"tool_calls"`
		} `json:"delta"`
	} `json:"choices"`
}

func parseOpenAIStream(body []byte) (OpenAIResult, error) {
	var result OpenAIResult

	// Accumulate tool call fragments by index.
	type toolFragment struct {
		name strings.Builder
		args strings.Builder
	}
	fragments := make(map[int]*toolFragment)

	var textBuf strings.Builder

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

		var chunk openAIChunk
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			continue
		}

		if chunk.Model != "" && result.Model == "" {
			result.Model = chunk.Model
		}

		for _, choice := range chunk.Choices {
			if choice.Delta.Content != "" {
				textBuf.WriteString(choice.Delta.Content)
			}
			for _, tc := range choice.Delta.ToolCalls {
				frag, ok := fragments[tc.Index]
				if !ok {
					frag = &toolFragment{}
					fragments[tc.Index] = frag
				}
				frag.name.WriteString(tc.Function.Name)
				frag.args.WriteString(tc.Function.Arguments)
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return OpenAIResult{}, err
	}

	if s := textBuf.String(); s != "" {
		result.AssistantText = append(result.AssistantText, s)
	}

	// Emit tool calls in index order.
	for i := 0; i < len(fragments); i++ {
		frag, ok := fragments[i]
		if !ok {
			continue
		}
		args := frag.args.String()
		if args == "" {
			args = "{}"
		}
		result.ToolCalls = append(result.ToolCalls, ToolCall{
			Name:  frag.name.String(),
			Input: args,
		})
	}

	return result, nil
}
