package serverless

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"go.uber.org/zap"
)

// mockInvocationLogger is a thread-safe InvocationLogger that records every
// record it receives. blockUntil, when non-nil, makes Log block until the
// channel is closed — used to keep the worker busy and force the bounded
// queue to fill.
type mockInvocationLogger struct {
	mu         sync.Mutex
	records    []*InvocationRecord
	calls      atomic.Int64
	blockUntil chan struct{}
}

func (m *mockInvocationLogger) Log(ctx context.Context, inv *InvocationRecord) error {
	m.calls.Add(1)
	if m.blockUntil != nil {
		select {
		case <-m.blockUntil:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	m.mu.Lock()
	m.records = append(m.records, inv)
	m.mu.Unlock()
	return nil
}

func (m *mockInvocationLogger) count() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.records)
}

// eventually polls cond up to timeout, failing the test if it never holds.
// Avoids a fixed sleep — we wait only as long as needed.
func eventually(t *testing.T, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("condition not met within %s", timeout)
}

func TestInvocationLogQueue_enqueue_is_nonblocking_and_records_reach_logger(t *testing.T) {
	sink := &mockInvocationLogger{}
	q := newInvocationLogQueue(sink, zap.NewNop())
	defer q.Close()

	rec := &InvocationRecord{ID: "inv-1", FunctionID: "fn-1", RequestID: "req-1"}
	if ok := q.enqueue(rec); !ok {
		t.Fatal("expected enqueue to accept the record")
	}

	eventually(t, time.Second, func() bool { return sink.count() == 1 })

	sink.mu.Lock()
	got := sink.records[0]
	sink.mu.Unlock()
	if got.ID != "inv-1" {
		t.Errorf("logger received wrong record: %+v", got)
	}
}

func TestInvocationLogQueue_enqueue_nil_is_noop(t *testing.T) {
	sink := &mockInvocationLogger{}
	q := newInvocationLogQueue(sink, zap.NewNop())
	defer q.Close()

	if ok := q.enqueue(nil); ok {
		t.Fatal("expected nil record to be rejected")
	}
}

func TestInvocationLogQueue_full_queue_drops_without_blocking_and_counts(t *testing.T) {
	// Hold the worker on the first record so the bounded channel fills, then
	// every further enqueue must drop (counted) without blocking.
	block := make(chan struct{})
	sink := &mockInvocationLogger{blockUntil: block}
	q := newInvocationLogQueue(sink, zap.NewNop())
	defer func() {
		close(block)
		q.Close()
	}()

	// First record is pulled by the worker and blocks there. The next
	// invocationLogQueueSize records fill the channel buffer.
	for i := 0; i < invocationLogQueueSize+1; i++ {
		_ = q.enqueue(&InvocationRecord{ID: "fill"})
	}
	// Wait until the worker has actually taken the first record so the buffer
	// is guaranteed full before we assert drops.
	eventually(t, time.Second, func() bool { return sink.calls.Load() >= 1 })

	// Now the channel is full; these must drop, and crucially must not block.
	const extra = 50
	done := make(chan struct{})
	go func() {
		for i := 0; i < extra; i++ {
			if q.enqueue(&InvocationRecord{ID: "overflow"}) {
				// Some may still squeak in if the worker drains; that's fine.
			}
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("enqueue blocked on a full queue")
	}

	if q.dropped.Load() == 0 {
		t.Fatal("expected at least one dropped record to be counted")
	}
}

func TestInvocationLogQueue_close_flushes_pending(t *testing.T) {
	sink := &mockInvocationLogger{}
	q := newInvocationLogQueue(sink, zap.NewNop())

	const n = 100
	for i := 0; i < n; i++ {
		q.enqueue(&InvocationRecord{ID: "inv"})
	}

	// Close must drain everything already queued before returning.
	q.Close()

	if got := sink.count(); got != n {
		t.Fatalf("expected Close to flush all %d records, got %d", n, got)
	}
}

func TestInvocationLogQueue_close_is_idempotent(t *testing.T) {
	sink := &mockInvocationLogger{}
	q := newInvocationLogQueue(sink, zap.NewNop())
	q.Close()
	q.Close() // must not panic on double close
}
