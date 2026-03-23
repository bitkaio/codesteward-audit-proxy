package audit

import (
	"context"
	"fmt"

	clickhouse "github.com/ClickHouse/clickhouse-go/v2"
)

// Writer writes batches of AuditEvents to ClickHouse.
type Writer struct {
	conn clickhouse.Conn
	db   string
}

// NewWriter opens a connection to ClickHouse and returns a Writer.
// The caller should call Ping to verify connectivity before use.
func NewWriter(dsn, db string) (*Writer, error) {
	opts, err := clickhouse.ParseDSN(dsn)
	if err != nil {
		return nil, fmt.Errorf("clickhouse: parse DSN: %w", err)
	}

	conn, err := clickhouse.Open(opts)
	if err != nil {
		return nil, fmt.Errorf("clickhouse: open: %w", err)
	}

	return &Writer{conn: conn, db: db}, nil
}

// Ping verifies connectivity to ClickHouse.
func (w *Writer) Ping(ctx context.Context) error {
	return w.conn.Ping(ctx)
}

// WriteBatch inserts a slice of AuditEvents in a single batch insert.
// Returns an error if the batch could not be sent; the caller decides how to
// handle it (log and discard — never block the proxy).
func (w *Writer) WriteBatch(ctx context.Context, events []AuditEvent) error {
	if len(events) == 0 {
		return nil
	}

	batch, err := w.conn.PrepareBatch(ctx,
		fmt.Sprintf("INSERT INTO %s.audit_events (session_id, turn_id, ts, agent, project, branch, direction, thinking, assistant_text, tool_name, tool_input, model, raw, request_captured, user_messages, resource_group, user, team)", w.db))
	if err != nil {
		return fmt.Errorf("clickhouse: prepare batch: %w", err)
	}

	for _, e := range events {
		var rc uint8
		if e.RequestCaptured {
			rc = 1
		}
		userMessages := e.UserMessages
		if userMessages == nil {
			userMessages = []string{}
		}
		if err := batch.Append(
			e.SessionID,
			e.TurnID,
			e.TS,
			e.Agent,
			e.Project,
			e.Branch,
			e.Direction,
			e.Thinking,
			e.AssistantText,
			e.ToolName,
			e.ToolInput,
			e.Model,
			e.Raw,
			rc,
			userMessages,
			e.ResourceGroup,
			e.User,
			e.Team,
		); err != nil {
			// Abort the whole batch on any row error.
			return fmt.Errorf("clickhouse: append row: %w", err)
		}
	}

	if err := batch.Send(); err != nil {
		return fmt.Errorf("clickhouse: send batch: %w", err)
	}

	return nil
}

// Close closes the underlying ClickHouse connection.
func (w *Writer) Close() error {
	return w.conn.Close()
}
