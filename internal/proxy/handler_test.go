package proxy

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"llm-audit-proxy/internal/audit"
)

// chanAdder implements audit.EventAdder by sending events to a channel.
type chanAdder struct {
	ch chan audit.AuditEvent
}

func (a *chanAdder) Add(e audit.AuditEvent) {
	select {
	case a.ch <- e:
	default:
	}
}

// forceUpstreamTransport rewrites every outbound request to the given test
// server URL so we can use a real Handler without real upstream DNS.
type forceUpstreamTransport struct {
	inner       http.RoundTripper
	upstreamURL string // e.g. "http://127.0.0.1:PORT"
}

func (t *forceUpstreamTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	r2 := req.Clone(req.Context())
	r2.URL.Host = strings.TrimPrefix(t.upstreamURL, "http://")
	r2.URL.Scheme = "http"
	r2.Host = r2.URL.Host
	return t.inner.RoundTrip(r2)
}

// newStack creates a Handler wired to a fake upstream and a chanAdder, then
// returns the handler and the event channel.
func newStack(t *testing.T, fakeHandler http.HandlerFunc) (*Handler, chan audit.AuditEvent) {
	t.Helper()
	upstream := httptest.NewServer(fakeHandler)
	t.Cleanup(upstream.Close)

	eventCh := make(chan audit.AuditEvent, 64)
	adder := &chanAdder{ch: eventCh}
	transport := &forceUpstreamTransport{
		inner:       http.DefaultTransport,
		upstreamURL: upstream.URL,
	}
	return NewHandler(adder, transport, "test-project", "test-branch", audit.NopScrubber{}, true, NewRouter("", "ml.hana.ondemand.com")), eventCh
}

// collectEvents drains eventCh until no new events arrive within 100 ms or
// the deadline (2 s) is hit.
func collectEvents(ch chan audit.AuditEvent) []audit.AuditEvent {
	var events []audit.AuditEvent
	deadline := time.After(2 * time.Second)
	for {
		select {
		case e := <-ch:
			events = append(events, e)
		case <-time.After(100 * time.Millisecond):
			return events
		case <-deadline:
			return events
		}
	}
}

// filterByDirection returns only the events with the given direction.
func filterByDirection(events []audit.AuditEvent, dir string) []audit.AuditEvent {
	var out []audit.AuditEvent
	for _, e := range events {
		if e.Direction == dir {
			out = append(out, e)
		}
	}
	return out
}

// --- Tests ------------------------------------------------------------------

func TestHandler_ProxiesStatus(t *testing.T) {
	handler, _ := newStack(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	})

	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader("{}"))
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusTeapot {
		t.Errorf("status: got %d, want %d", rr.Code, http.StatusTeapot)
	}
}

func TestHandler_ProxiesBody(t *testing.T) {
	const body = `{"type":"message","content":[]}`
	handler, _ := newStack(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, body)
	})

	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader("{}"))
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if got := rr.Body.String(); got != body {
		t.Errorf("body: got %q, want %q", got, body)
	}
}

func TestHandler_ProxiesResponseHeaders(t *testing.T) {
	handler, _ := newStack(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Custom-Header", "proxied")
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/v1/messages", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if got := rr.Header().Get("X-Custom-Header"); got != "proxied" {
		t.Errorf("X-Custom-Header: got %q, want %q", got, "proxied")
	}
}

func TestHandler_StripsInternalHeadersFromUpstream(t *testing.T) {
	var gotSessionID, gotAuditAgent string
	handler, _ := newStack(t, func(w http.ResponseWriter, r *http.Request) {
		gotSessionID = r.Header.Get("X-Session-ID")
		gotAuditAgent = r.Header.Get("X-Audit-Agent")
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	req.Header.Set("X-Session-ID", "my-session")
	req.Header.Set("X-Audit-Agent", "claude-code")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if gotSessionID != "" {
		t.Errorf("X-Session-ID leaked to upstream: %q", gotSessionID)
	}
	if gotAuditAgent != "" {
		t.Errorf("X-Audit-Agent leaked to upstream: %q", gotAuditAgent)
	}
}

func TestHandler_EmitsAuditEventWithToolCall(t *testing.T) {
	upstreamBody, _ := json.Marshal(map[string]any{
		"model": "claude-opus-4-6",
		"content": []any{
			map[string]any{"type": "text", "text": "Sure!"},
			map[string]any{
				"type":  "tool_use",
				"name":  "Write",
				"input": map[string]any{"file_path": "main.go"},
			},
		},
	})

	handler, eventCh := newStack(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write(upstreamBody)
	})

	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader("{}"))
	req.Header.Set("X-Session-ID", "sess-123")
	req.Header.Set("User-Agent", "claude-code/1.0")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	all := collectEvents(eventCh)
	events := filterByDirection(all, "response")
	if len(events) == 0 {
		t.Fatal("no response audit events emitted")
	}

	// All response events share the same session / agent / model.
	e := events[0]
	if e.SessionID != "sess-123" {
		t.Errorf("session_id: got %q", e.SessionID)
	}
	if e.Agent != "claude-code" {
		t.Errorf("agent: got %q", e.Agent)
	}
	if e.Model != "claude-opus-4-6" {
		t.Errorf("model: got %q", e.Model)
	}

	// Expect exactly one response event for the tool call.
	var toolEvent *audit.AuditEvent
	for i := range events {
		if events[i].ToolName != "" {
			toolEvent = &events[i]
			break
		}
	}
	if toolEvent == nil {
		t.Fatal("no tool call event emitted")
	}
	if toolEvent.ToolName != "Write" {
		t.Errorf("tool_name: got %q, want %q", toolEvent.ToolName, "Write")
	}
}

func TestHandler_EmitsAuditEventNoToolCall(t *testing.T) {
	upstreamBody, _ := json.Marshal(map[string]any{
		"model":   "claude-opus-4-6",
		"content": []any{map[string]any{"type": "text", "text": "Hi!"}},
	})

	handler, eventCh := newStack(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write(upstreamBody)
	})

	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader("{}"))
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	all := collectEvents(eventCh)
	events := filterByDirection(all, "response")
	if len(events) != 1 {
		t.Fatalf("response events: got %d, want 1", len(events))
	}
	if events[0].ToolName != "" {
		t.Errorf("tool_name should be empty, got %q", events[0].ToolName)
	}
}

func TestHandler_SessionIDPropagated(t *testing.T) {
	handler, eventCh := newStack(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"model":"m","content":[]}`))
	})

	req := httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	req.Header.Set("X-Session-ID", "fixed-session-id")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	events := collectEvents(eventCh)
	if len(events) == 0 {
		t.Fatal("no events")
	}
	if events[0].SessionID != "fixed-session-id" {
		t.Errorf("session_id: got %q, want %q", events[0].SessionID, "fixed-session-id")
	}
}

func TestHandler_BadGatewayOnUnreachableUpstream(t *testing.T) {
	dead := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	dead.Close() // immediately close so the port is unreachable

	eventCh := make(chan audit.AuditEvent, 16)
	handler := NewHandler(&chanAdder{ch: eventCh}, &forceUpstreamTransport{
		inner:       http.DefaultTransport,
		upstreamURL: dead.URL,
	}, "", "", audit.NopScrubber{}, true, NewRouter("", "ml.hana.ondemand.com"))

	req := httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadGateway {
		t.Errorf("status: got %d, want %d", rr.Code, http.StatusBadGateway)
	}
}
