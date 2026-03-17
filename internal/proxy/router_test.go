package proxy

import (
	"net/http"
	"net/url"
	"testing"
)

func newReq(host, path string) *http.Request {
	r := &http.Request{
		Host:   host,
		Header: make(http.Header),
		URL:    &url.URL{Path: path},
	}
	return r
}

// --- DetectUpstream ---------------------------------------------------------

func TestDetectUpstream_HostAnthropic(t *testing.T) {
	r := newReq("api.anthropic.com", "/v1/messages")
	up := DetectUpstream(r)
	if up.Name != "anthropic" {
		t.Errorf("got %q, want %q", up.Name, "anthropic")
	}
}

func TestDetectUpstream_HostOpenAI(t *testing.T) {
	r := newReq("api.openai.com", "/v1/chat/completions")
	up := DetectUpstream(r)
	if up.Name != "openai" {
		t.Errorf("got %q, want %q", up.Name, "openai")
	}
}

func TestDetectUpstream_HostGemini(t *testing.T) {
	r := newReq("generativelanguage.googleapis.com", "/v1beta/models/gemini-pro:generateContent")
	up := DetectUpstream(r)
	if up.Name != "gemini" {
		t.Errorf("got %q, want %q", up.Name, "gemini")
	}
}

func TestDetectUpstream_HostWithPort(t *testing.T) {
	// When agent sets ANTHROPIC_BASE_URL=http://localhost:8080 the Host header
	// is "localhost:8080", so routing falls through to path-based detection.
	r := newReq("localhost:8080", "/v1/messages")
	up := DetectUpstream(r)
	if up.Name != "anthropic" {
		t.Errorf("got %q, want %q", up.Name, "anthropic")
	}
}

func TestDetectUpstream_PathMessages(t *testing.T) {
	r := newReq("localhost:8080", "/v1/messages")
	up := DetectUpstream(r)
	if up.Name != "anthropic" {
		t.Errorf("got %q, want %q", up.Name, "anthropic")
	}
}

func TestDetectUpstream_PathChatCompletions(t *testing.T) {
	r := newReq("localhost:8080", "/v1/chat/completions")
	up := DetectUpstream(r)
	if up.Name != "openai" {
		t.Errorf("got %q, want %q", up.Name, "openai")
	}
}

func TestDetectUpstream_PathV1Beta(t *testing.T) {
	r := newReq("localhost:8080", "/v1beta/models/gemini-pro:generateContent")
	up := DetectUpstream(r)
	if up.Name != "gemini" {
		t.Errorf("got %q, want %q", up.Name, "gemini")
	}
}

func TestDetectUpstream_PathAnthropicPrefix(t *testing.T) {
	r := newReq("localhost:8080", "/anthropic/v1/messages")
	up := DetectUpstream(r)
	if up.Name != "anthropic" {
		t.Errorf("got %q, want %q", up.Name, "anthropic")
	}
}

func TestDetectUpstream_AnthropicVersionHeader(t *testing.T) {
	r := newReq("localhost:8080", "/unknown/path")
	r.Header.Set("anthropic-version", "2023-06-01")
	up := DetectUpstream(r)
	if up.Name != "anthropic" {
		t.Errorf("got %q, want %q", up.Name, "anthropic")
	}
}

func TestDetectUpstream_DefaultIsAnthropic(t *testing.T) {
	r := newReq("localhost:8080", "/")
	up := DetectUpstream(r)
	if up.Name != "anthropic" {
		t.Errorf("default upstream: got %q, want %q", up.Name, "anthropic")
	}
}

// --- RewriteRequest ---------------------------------------------------------

func TestRewriteRequest_SetsSchemeAndHost(t *testing.T) {
	r := newReq("localhost:8080", "/v1/messages")
	r.URL = &url.URL{Path: "/v1/messages", RawQuery: "stream=true"}

	RewriteRequest(r, anthropicUpstream)

	if r.URL.Scheme != "https" {
		t.Errorf("scheme: got %q, want %q", r.URL.Scheme, "https")
	}
	if r.URL.Host != "api.anthropic.com" {
		t.Errorf("host: got %q, want %q", r.URL.Host, "api.anthropic.com")
	}
	if r.Host != "api.anthropic.com" {
		t.Errorf("r.Host: got %q, want %q", r.Host, "api.anthropic.com")
	}
	// Query string must be preserved.
	if r.URL.RawQuery != "stream=true" {
		t.Errorf("query: got %q", r.URL.RawQuery)
	}
}

func TestRewriteRequest_StripsAnthropicPrefix(t *testing.T) {
	r := newReq("localhost:8080", "/anthropic/v1/messages")
	r.URL = &url.URL{Path: "/anthropic/v1/messages"}

	RewriteRequest(r, anthropicUpstream)

	if r.URL.Path != "/v1/messages" {
		t.Errorf("path: got %q, want %q", r.URL.Path, "/v1/messages")
	}
}

func TestRewriteRequest_StripsOpenAIPrefix(t *testing.T) {
	r := newReq("localhost:8080", "/openai/v1/chat/completions")
	r.URL = &url.URL{Path: "/openai/v1/chat/completions"}

	RewriteRequest(r, openaiUpstream)

	if r.URL.Path != "/v1/chat/completions" {
		t.Errorf("path: got %q, want %q", r.URL.Path, "/v1/chat/completions")
	}
}

func TestRewriteRequest_PreservesNonPrefixedPath(t *testing.T) {
	r := newReq("localhost:8080", "/v1/messages")
	r.URL = &url.URL{Path: "/v1/messages"}

	RewriteRequest(r, anthropicUpstream)

	if r.URL.Path != "/v1/messages" {
		t.Errorf("path should be unchanged: got %q", r.URL.Path)
	}
}
