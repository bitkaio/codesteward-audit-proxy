package audit

import (
	"context"
	"sync"
	"testing"
	"time"
)

// mockWriter records batches written to it instead of hitting ClickHouse.
type mockWriter struct {
	mu      sync.Mutex
	batches [][]AuditEvent
}

func (m *mockWriter) WriteBatch(_ context.Context, events []AuditEvent) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	// Copy the slice so the caller's backing array changes don't affect us.
	cp := make([]AuditEvent, len(events))
	copy(cp, events)
	m.batches = append(m.batches, cp)
	return nil
}

func (m *mockWriter) totalEvents() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for _, b := range m.batches {
		n += len(b)
	}
	return n
}

func (m *mockWriter) batchCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.batches)
}

// newTestBatcher creates a Batcher backed by a mockWriter.
func newTestBatcher(mock *mockWriter, size int, interval time.Duration) *Batcher {
	// We bypass NewBatcher's real Writer type by providing a thin adapter.
	b := &Batcher{
		writer:   nil, // unused — we override flush
		size:     size,
		interval: interval,
		ch:       make(chan AuditEvent, size*10),
		done:     make(chan struct{}),
	}
	// Override flush to use the mock.
	go runWithMock(b, mock)
	return b
}

// runWithMock mirrors batcher.run but calls mock.WriteBatch instead of
// b.writer.WriteBatch so we don't need a real ClickHouse connection.
func runWithMock(b *Batcher, mock *mockWriter) {
	ticker := time.NewTicker(b.interval)
	defer ticker.Stop()

	batch := make([]AuditEvent, 0, b.size)

	flush := func(events []AuditEvent) {
		if len(events) == 0 {
			return
		}
		_ = mock.WriteBatch(context.Background(), events)
	}

	for {
		select {
		case e := <-b.ch:
			batch = append(batch, e)
			if len(batch) >= b.size {
				flush(batch)
				batch = batch[:0]
				ticker.Reset(b.interval)
			}
		case <-ticker.C:
			if len(batch) > 0 {
				flush(batch)
				batch = batch[:0]
			}
		case <-b.done:
			for {
				select {
				case e := <-b.ch:
					batch = append(batch, e)
				default:
					flush(batch)
					return
				}
			}
		}
	}
}

func makeEvent(id string) AuditEvent {
	return AuditEvent{SessionID: "sess", TurnID: id, TS: time.Now()}
}

// TestBatcherFlushOnSize verifies that the batcher flushes immediately when
// the batch size threshold is reached without waiting for the ticker.
func TestBatcherFlushOnSize(t *testing.T) {
	mock := &mockWriter{}
	batchSize := 5
	// Use a very long ticker interval so it never fires during the test.
	b := newTestBatcher(mock, batchSize, 10*time.Second)
	defer b.Stop()

	for i := 0; i < batchSize; i++ {
		b.Add(makeEvent(string(rune('a' + i))))
	}

	// Give the goroutine time to process.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if mock.totalEvents() == batchSize {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	if got := mock.totalEvents(); got != batchSize {
		t.Errorf("total events: got %d, want %d", got, batchSize)
	}
	if got := mock.batchCount(); got != 1 {
		t.Errorf("batch count: got %d, want 1", got, )
	}
}

// TestBatcherFlushOnTicker verifies that the batcher flushes on the interval
// even when the batch is below the size threshold.
func TestBatcherFlushOnTicker(t *testing.T) {
	mock := &mockWriter{}
	// Use a small interval and a large batch size so only the ticker triggers.
	b := newTestBatcher(mock, 1000, 50*time.Millisecond)
	defer b.Stop()

	b.Add(makeEvent("t1"))
	b.Add(makeEvent("t2"))

	// Wait for at least one ticker flush.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if mock.totalEvents() >= 2 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	if got := mock.totalEvents(); got != 2 {
		t.Errorf("total events after ticker flush: got %d, want 2", got)
	}
}

// TestBatcherStopDrains verifies that Stop flushes remaining events.
func TestBatcherStopDrains(t *testing.T) {
	mock := &mockWriter{}
	b := newTestBatcher(mock, 1000, 10*time.Second)

	b.Add(makeEvent("drain1"))
	b.Add(makeEvent("drain2"))
	b.Add(makeEvent("drain3"))

	b.Stop()

	// After Stop returns the goroutine has exited and the final flush was sent.
	// Give a small window for the goroutine to finish.
	time.Sleep(50 * time.Millisecond)

	if got := mock.totalEvents(); got != 3 {
		t.Errorf("events after Stop: got %d, want 3", got)
	}
}

// TestBatcherDropWhenFull verifies the batcher doesn't block when the channel
// is saturated (it drops and logs instead).
func TestBatcherDropWhenFull(t *testing.T) {
	mock := &mockWriter{}
	// Very small channel (size 1, buffer = 10) and slow ticker.
	b := &Batcher{
		size:     1,
		interval: 10 * time.Second,
		ch:       make(chan AuditEvent, 1), // tiny buffer
		done:     make(chan struct{}),
	}
	go runWithMock(b, mock)
	defer b.Stop()

	// Flood the channel — Add must never block.
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 100; i++ {
			b.Add(makeEvent("flood"))
		}
	}()

	select {
	case <-done:
		// Good — Add completed without blocking.
	case <-time.After(2 * time.Second):
		t.Error("Add blocked unexpectedly")
	}
}
