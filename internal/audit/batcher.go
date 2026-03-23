package audit

import (
	"context"
	"log/slog"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
)

// Batcher accumulates AuditEvents in memory and flushes them to ClickHouse
// either when the batch reaches a size threshold or a time interval elapses,
// whichever comes first.
//
// All writes to the batcher are non-blocking: if the internal channel is full
// the event is dropped and logged. This ensures the batcher never stalls the
// proxy request path.
type Batcher struct {
	writer   *Writer
	size     int
	interval time.Duration

	ch      chan AuditEvent
	done    chan struct{}
	flushFn func([]AuditEvent) // overrides writer when set (used in tests)
}

// NewChannelBatcher creates a Batcher that routes flushed events directly into
// out instead of writing to ClickHouse. Intended for testing.
func NewChannelBatcher(out chan AuditEvent) *Batcher {
	b := &Batcher{
		size:     100,
		interval: time.Second,
		ch:       make(chan AuditEvent, 1000),
		done:     make(chan struct{}),
		flushFn: func(events []AuditEvent) {
			for _, e := range events {
				select {
				case out <- e:
				default:
				}
			}
		},
	}
	go b.run()
	return b
}

// NewBatcher creates and starts a Batcher. Call Stop to drain and shut it down.
func NewBatcher(writer *Writer, size int, interval time.Duration) *Batcher {
	b := &Batcher{
		writer:   writer,
		size:     size,
		interval: interval,
		// Buffer 10× batch size so Add never blocks under normal load.
		ch:   make(chan AuditEvent, size*10),
		done: make(chan struct{}),
	}
	go b.run()
	return b
}

// Add enqueues an event. It never blocks; if the channel is full the event is
// discarded and a warning is logged.
func (b *Batcher) Add(e AuditEvent) {
	select {
	case b.ch <- e:
	default:
		slog.Warn("audit batcher channel full, dropping event",
			"session_id", e.SessionID,
			"turn_id", e.TurnID,
		)
	}
}

// Stop drains any remaining events and shuts down the background goroutine.
func (b *Batcher) Stop() {
	close(b.done)
}

func (b *Batcher) run() {
	ticker := time.NewTicker(b.interval)
	defer ticker.Stop()

	batch := make([]AuditEvent, 0, b.size)

	for {
		select {
		case e := <-b.ch:
			batch = append(batch, e)
			if len(batch) >= b.size {
				b.flush(batch)
				batch = batch[:0]
				ticker.Reset(b.interval)
			}

		case <-ticker.C:
			if len(batch) > 0 {
				b.flush(batch)
				batch = batch[:0]
			}

		case <-b.done:
			// Drain whatever is left in the channel before exiting.
			for {
				select {
				case e := <-b.ch:
					batch = append(batch, e)
				default:
					if len(batch) > 0 {
						b.flush(batch)
					}
					return
				}
			}
		}
	}
}

// UnprocessedBatcher accumulates UnprocessedEvents and flushes them to
// ClickHouse. Same design as Batcher: non-blocking Add, background goroutine.
type UnprocessedBatcher struct {
	writer   *Writer
	size     int
	interval time.Duration
	ch       chan UnprocessedEvent
	done     chan struct{}
}

// NewUnprocessedBatcher creates and starts an UnprocessedBatcher.
func NewUnprocessedBatcher(writer *Writer, size int, interval time.Duration) *UnprocessedBatcher {
	b := &UnprocessedBatcher{
		writer:   writer,
		size:     size,
		interval: interval,
		ch:       make(chan UnprocessedEvent, size*10),
		done:     make(chan struct{}),
	}
	go b.run()
	return b
}

func (b *UnprocessedBatcher) Add(e UnprocessedEvent) {
	select {
	case b.ch <- e:
	default:
		slog.Warn("unprocessed batcher channel full, dropping event",
			"session_id", e.SessionID,
			"turn_id", e.TurnID,
		)
	}
}

func (b *UnprocessedBatcher) Stop() {
	close(b.done)
}

func (b *UnprocessedBatcher) run() {
	ticker := time.NewTicker(b.interval)
	defer ticker.Stop()

	batch := make([]UnprocessedEvent, 0, b.size)

	for {
		select {
		case e := <-b.ch:
			batch = append(batch, e)
			if len(batch) >= b.size {
				b.flush(batch)
				batch = batch[:0]
				ticker.Reset(b.interval)
			}
		case <-ticker.C:
			if len(batch) > 0 {
				b.flush(batch)
				batch = batch[:0]
			}
		case <-b.done:
			for {
				select {
				case e := <-b.ch:
					batch = append(batch, e)
				default:
					if len(batch) > 0 {
						b.flush(batch)
					}
					return
				}
			}
		}
	}
}

func (b *UnprocessedBatcher) flush(batch []UnprocessedEvent) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	start := time.Now()
	if err := b.writer.WriteUnprocessedBatch(ctx, batch); err != nil {
		slog.Error("clickhouse unprocessed batch write failed, discarding batch",
			"err", err,
			"count", len(batch),
		)
		return
	}

	slog.Debug("clickhouse unprocessed batch flushed",
		"count", len(batch),
		"duration_ms", time.Since(start).Milliseconds(),
	)
}

func (b *Batcher) flush(batch []AuditEvent) {
	if b.flushFn != nil {
		b.flushFn(batch)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	ctx, span := otel.Tracer("llm-audit-proxy").Start(ctx, "audit.batch.flush")
	span.SetAttributes(
		attribute.Int("batch.size", len(batch)),
		attribute.String("db.system", "clickhouse"),
	)
	defer span.End()

	start := time.Now()
	if err := b.writer.WriteBatch(ctx, batch); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		slog.Error("clickhouse batch write failed, discarding batch",
			"err", err,
			"count", len(batch),
		)
		return
	}

	slog.Info("clickhouse batch flushed",
		"count", len(batch),
		"duration_ms", time.Since(start).Milliseconds(),
	)
}
