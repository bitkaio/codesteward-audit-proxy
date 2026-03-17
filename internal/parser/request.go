package parser

import (
	"encoding/json"

	"llm-audit-proxy/internal/audit"
)

const requestCaptureDisabled = "[request capture disabled]"

// RequestResult holds the audit-relevant fields extracted from a request body.
type RequestResult struct {
	// UserMessages contains the text content of every role:"user" message,
	// after scrubbing. Always empty when RequestCaptured is false.
	UserMessages []string

	// Raw is the request body after scrubbing, or the capture-disabled
	// placeholder when RequestCaptured is false.
	Raw string

	// RequestCaptured is false when captureRequests was false.
	RequestCaptured bool
}

// ParseRequest extracts user-role message content from a request body.
// It is provider-agnostic: both Anthropic and OpenAI use a messages[] array
// with role/content fields, so a single parser handles both.
//
// When captureRequests is false the body is never read into audit storage;
// raw is replaced with a placeholder and UserMessages is left empty.
//
// The scrubber is applied to each extracted user message and to the raw field.
// The original body bytes are never modified — scrubbing only affects the
// values returned in RequestResult.
func ParseRequest(body []byte, scrubber audit.Scrubber, captureRequests bool) RequestResult {
	if !captureRequests {
		return RequestResult{
			Raw:             requestCaptureDisabled,
			UserMessages:    []string{},
			RequestCaptured: false,
		}
	}

	userMessages := extractUserMessages(body, scrubber)

	return RequestResult{
		Raw:             scrubber.Scrub(string(body)),
		UserMessages:    userMessages,
		RequestCaptured: true,
	}
}

// requestBody is the minimal shape shared by Anthropic and OpenAI request
// formats. Both use a top-level "messages" array with "role" and "content".
type requestBody struct {
	Messages []requestMessage `json:"messages"`
}

type requestMessage struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

// contentBlock represents an element in an array-form content field.
// Only type:"text" blocks are extracted.
type contentBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// extractUserMessages parses the messages array and returns all text content
// from role:"user" entries, with the scrubber applied to each string.
func extractUserMessages(body []byte, scrubber audit.Scrubber) []string {
	var req requestBody
	if err := json.Unmarshal(body, &req); err != nil {
		return []string{}
	}

	var out []string
	for _, msg := range req.Messages {
		if msg.Role != "user" {
			continue
		}
		for _, text := range extractTextFromContent(msg.Content) {
			out = append(out, scrubber.Scrub(text))
		}
	}
	if out == nil {
		return []string{}
	}
	return out
}

// extractTextFromContent handles content that is either a plain JSON string
// or an array of content blocks (Anthropic-style). Only type:"text" blocks
// are returned from array form.
func extractTextFromContent(raw json.RawMessage) []string {
	if len(raw) == 0 {
		return nil
	}

	// Try plain string first.
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		if s != "" {
			return []string{s}
		}
		return nil
	}

	// Try array of content blocks.
	var blocks []contentBlock
	if err := json.Unmarshal(raw, &blocks); err != nil {
		return nil
	}
	var out []string
	for _, b := range blocks {
		if b.Type == "text" && b.Text != "" {
			out = append(out, b.Text)
		}
	}
	return out
}
