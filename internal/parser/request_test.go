package parser

import (
	"reflect"
	"testing"

	"llm-audit-proxy/internal/audit"
)

// --- helpers ----------------------------------------------------------------

func nop() audit.Scrubber { return audit.NopScrubber{} }

func patternScrubber(t *testing.T, patterns ...string) audit.Scrubber {
	t.Helper()
	s, err := audit.NewPatternScrubber(patterns)
	if err != nil {
		t.Fatalf("NewPatternScrubber: %v", err)
	}
	return s
}

// --- capture disabled -------------------------------------------------------

func TestParseRequest_CaptureDisabled(t *testing.T) {
	body := []byte(`{"messages":[{"role":"user","content":"hello"}]}`)
	result := ParseRequest(body, nop(), false)

	if result.RequestCaptured {
		t.Error("RequestCaptured should be false")
	}
	if result.Raw != "[request capture disabled]" {
		t.Errorf("Raw = %q, want placeholder", result.Raw)
	}
	if len(result.UserMessages) != 0 {
		t.Errorf("UserMessages should be empty, got %v", result.UserMessages)
	}
}

// --- Anthropic format -------------------------------------------------------

func TestParseRequest_Anthropic_StringContent(t *testing.T) {
	body := []byte(`{
		"model": "claude-opus-4-6",
		"messages": [
			{"role": "user", "content": "What is Go?"}
		]
	}`)
	result := ParseRequest(body, nop(), true)

	if !result.RequestCaptured {
		t.Error("RequestCaptured should be true")
	}
	want := []string{"What is Go?"}
	if !reflect.DeepEqual(result.UserMessages, want) {
		t.Errorf("UserMessages = %v, want %v", result.UserMessages, want)
	}
}

func TestParseRequest_Anthropic_ArrayContentTextOnly(t *testing.T) {
	body := []byte(`{
		"model": "claude-opus-4-6",
		"messages": [
			{"role": "user", "content": [
				{"type": "text",  "text": "Describe this image"},
				{"type": "image", "source": {"type": "base64", "media_type": "image/png", "data": "abc"}}
			]}
		]
	}`)
	result := ParseRequest(body, nop(), true)

	want := []string{"Describe this image"}
	if !reflect.DeepEqual(result.UserMessages, want) {
		t.Errorf("UserMessages = %v, want %v (image block must be ignored)", result.UserMessages, want)
	}
}

func TestParseRequest_Anthropic_MultipleTextBlocks(t *testing.T) {
	body := []byte(`{
		"messages": [
			{"role": "user", "content": [
				{"type": "text", "text": "First part"},
				{"type": "text", "text": "Second part"}
			]}
		]
	}`)
	result := ParseRequest(body, nop(), true)

	want := []string{"First part", "Second part"}
	if !reflect.DeepEqual(result.UserMessages, want) {
		t.Errorf("UserMessages = %v, want %v", result.UserMessages, want)
	}
}

// --- OpenAI format ----------------------------------------------------------

func TestParseRequest_OpenAI_UserMessages(t *testing.T) {
	body := []byte(`{
		"model": "gpt-4",
		"messages": [
			{"role": "system", "content": "You are helpful"},
			{"role": "user",   "content": "Tell me a joke"}
		]
	}`)
	result := ParseRequest(body, nop(), true)

	want := []string{"Tell me a joke"}
	if !reflect.DeepEqual(result.UserMessages, want) {
		t.Errorf("UserMessages = %v, want %v", result.UserMessages, want)
	}
}

// --- mixed conversation -----------------------------------------------------

func TestParseRequest_MixedConversation_OnlyUserExtracted(t *testing.T) {
	body := []byte(`{
		"messages": [
			{"role": "user",      "content": "First user message"},
			{"role": "assistant", "content": "I can help with that"},
			{"role": "user",      "content": "Second user message"},
			{"role": "assistant", "content": "Sure!"}
		]
	}`)
	result := ParseRequest(body, nop(), true)

	want := []string{"First user message", "Second user message"}
	if !reflect.DeepEqual(result.UserMessages, want) {
		t.Errorf("UserMessages = %v, want %v", result.UserMessages, want)
	}
}

// --- scrubber applied -------------------------------------------------------

func TestParseRequest_ScrubberAppliedToUserMessages(t *testing.T) {
	s := patternScrubber(t, `sk-[a-zA-Z0-9]{32,}`)

	body := []byte(`{
		"messages": [
			{"role": "user", "content": "my key is sk-abcdefghijklmnopqrstuvwxyz123456 thanks"}
		]
	}`)
	result := ParseRequest(body, s, true)

	want := []string{"my key is [REDACTED] thanks"}
	if !reflect.DeepEqual(result.UserMessages, want) {
		t.Errorf("UserMessages = %v, want %v", result.UserMessages, want)
	}
}

func TestParseRequest_ScrubberAppliedToRaw(t *testing.T) {
	s := patternScrubber(t, `sk-[a-zA-Z0-9]{32,}`)

	body := []byte(`{"messages":[{"role":"user","content":"key sk-abcdefghijklmnopqrstuvwxyz123456"}]}`)
	result := ParseRequest(body, s, true)

	if result.Raw == string(body) {
		t.Error("Raw should have been scrubbed but is identical to original body")
	}
	// Confirm [REDACTED] appears in the scrubbed raw.
	if !contains(result.Raw, "[REDACTED]") {
		t.Errorf("expected [REDACTED] in Raw, got %q", result.Raw)
	}
}

func TestParseRequest_EmptyBody(t *testing.T) {
	result := ParseRequest([]byte{}, nop(), true)

	if !result.RequestCaptured {
		t.Error("RequestCaptured should be true")
	}
	if len(result.UserMessages) != 0 {
		t.Errorf("UserMessages should be empty for empty body, got %v", result.UserMessages)
	}
}

func TestParseRequest_NoMessagesField(t *testing.T) {
	body := []byte(`{"model":"claude-opus-4-6","max_tokens":1024}`)
	result := ParseRequest(body, nop(), true)

	if len(result.UserMessages) != 0 {
		t.Errorf("UserMessages should be empty when no messages field, got %v", result.UserMessages)
	}
	if result.Raw != string(body) {
		t.Error("Raw should equal original body when no scrubbing needed")
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(substr) == 0 ||
		func() bool {
			for i := 0; i <= len(s)-len(substr); i++ {
				if s[i:i+len(substr)] == substr {
					return true
				}
			}
			return false
		}())
}
