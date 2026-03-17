package audit

import "time"

// EventAdder is the minimal interface the proxy handler needs from a batcher.
// Using an interface here keeps the handler testable without a real ClickHouse
// connection.
type EventAdder interface {
	Add(AuditEvent)
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

	// RequestCaptured is false when AUDIT_CAPTURE_REQUESTS=false.
	// Always true on direction="response" records.
	RequestCaptured bool

	// UserMessages holds extracted user-role message content, scrubbed.
	// Populated only on direction="request" records.
	UserMessages []string
}
