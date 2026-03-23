package audit

import "time"

// EventAdder is the minimal interface the proxy handler needs from a batcher.
// Using an interface here keeps the handler testable without a real ClickHouse
// connection.
type EventAdder interface {
	Add(AuditEvent)
}

// UnprocessedAdder is the interface for routing unparseable events to a
// separate store.
type UnprocessedAdder interface {
	Add(UnprocessedEvent)
}

// UnprocessedEvent captures requests/responses the proxy could not parse
// into structured AuditEvents. Stored in a separate ClickHouse table to
// keep the main audit_events table clean while retaining raw data for
// debugging parsing issues and discovering new endpoints.
type UnprocessedEvent struct {
	SessionID   string
	TurnID      string
	TS          time.Time
	Agent       string
	Project     string
	Branch      string
	User        string
	Team        string
	Direction   string
	Method      string // HTTP method (GET, POST, etc.)
	Path        string // Request URL path
	StatusCode  int    // HTTP response status code (0 for request-direction events)
	ContentType string // Response Content-Type header
	Raw         string // Full raw body
	Error       string // Parse error message, if any
}

// AuditEvent is one row in the audit_events ClickHouse table.
// One event is produced per tool call. Responses with no tool calls produce
// a single event with ToolName = "".
type AuditEvent struct {
	SessionID     string
	TurnID        string
	TS            time.Time
	Agent         string
	Project       string
	Branch        string
	Direction     string
	Thinking      []string
	AssistantText []string
	ToolName      string
	ToolInput     string // JSON-encoded tool input object
	Model         string
	Raw           string // full original response body

	// User is the developer identity injected by the IDE plugin via
	// the X-Audit-User header. Empty when no plugin is configured.
	User string

	// Team is the team/org identifier injected via X-Audit-Team header.
	Team string

	// ResourceGroup is the SAP AI Core AI-Resource-Group header value.
	// Empty for all non-SAP providers.
	ResourceGroup string

	// RequestCaptured is false when AUDIT_CAPTURE_REQUESTS=false.
	// Always true on direction="response" records.
	RequestCaptured bool

	// UserMessages holds extracted user-role message content, scrubbed.
	// Populated only on direction="request" records.
	UserMessages []string
}
