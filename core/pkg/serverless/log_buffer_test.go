package serverless

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
)

// TestLogBuffer_appendAndSnapshot verifies the basic Append → Snapshot
// roundtrip. The snapshot must be a defensive copy so mutating it
// doesn't corrupt the buffer's internal state.
func TestLogBuffer_appendAndSnapshot(t *testing.T) {
	b := NewLogBuffer()
	b.Append(LogEntry{Level: "info", Message: "hello"})
	b.Append(LogEntry{Level: "error", Message: "boom"})

	snap := b.Snapshot()
	if len(snap) != 2 {
		t.Fatalf("snapshot len = %d; want 2", len(snap))
	}
	if snap[0].Message != "hello" || snap[1].Message != "boom" {
		t.Errorf("snapshot order wrong: %+v", snap)
	}

	// Mutate the snapshot — buffer must be unaffected.
	snap[0].Message = "MUTATED"
	freshSnap := b.Snapshot()
	if freshSnap[0].Message != "hello" {
		t.Errorf("snapshot must be defensive copy; buffer was mutated: %+v", freshSnap)
	}
}

// TestWithLogBuffer_extractsAttachedBuffer is the basic ctx-attachment
// round-trip. Anything more sophisticated (cross-call propagation) is
// validated end-to-end in the host-functions tests.
func TestWithLogBuffer_extractsAttachedBuffer(t *testing.T) {
	b := NewLogBuffer()
	ctx := WithLogBuffer(context.Background(), b)

	got := LogBufferFromCtx(ctx)
	if got != b {
		t.Errorf("LogBufferFromCtx returned %p; want %p", got, b)
	}
}

// TestWithLogBuffer_nilIsNoop guards the contract that passing nil
// returns ctx unchanged. Important because the call site in Engine.Execute
// always passes a non-nil buffer, but tests and back-compat callers
// might pass nil and expect ctx untouched (and LogBufferFromCtx to
// return nil so logging falls back to the singleton).
func TestWithLogBuffer_nilIsNoop(t *testing.T) {
	ctx := WithLogBuffer(context.Background(), nil)
	if got := LogBufferFromCtx(ctx); got != nil {
		t.Errorf("LogBufferFromCtx after WithLogBuffer(nil) = %p; want nil", got)
	}
}

// TestLogBufferFromCtx_nilCtxIsSafe — defensive guard. ctx-key lookup
// on a nil ctx panics if not handled.
func TestLogBufferFromCtx_nilCtxIsSafe(t *testing.T) {
	if got := LogBufferFromCtx(nil); got != nil {
		t.Errorf("LogBufferFromCtx(nil) = %p; want nil", got)
	}
}

// TestLogBuffer_concurrentAppendIsSafe stresses the lock contract. The
// bug we're fixing (bugboard #108) was about state being shared across
// goroutines without locking — this test asserts the FIX doesn't
// reintroduce a different race in its own internal state.
//
// Run with -race for stronger signal. Without the mutex inside Append,
// the race detector would flag this.
func TestLogBuffer_concurrentAppendIsSafe(t *testing.T) {
	b := NewLogBuffer()
	// Keep total below maxLogEntriesPerInvocation — this test pins
	// race-safety (no lost writes), not the cap (covered separately in
	// log_buffer_cap_test.go).
	const (
		writers    = 16
		writesPerW = 50
	)
	var wg sync.WaitGroup
	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for n := 0; n < writesPerW; n++ {
				b.Append(LogEntry{Level: "info", Message: "x"})
			}
		}(w)
	}
	wg.Wait()

	got := b.Len()
	want := writers * writesPerW
	if got != want {
		t.Errorf("Len after concurrent writes = %d; want %d (lost writes — race)", got, want)
	}
}

// TestLogBuffer_concurrentInvocationsDoNotCrossContaminate is the
// REGRESSION GUARD for bugboard #108. Two goroutines simulating
// concurrent invocations each create their OWN LogBuffer attached to
// their OWN ctx. They append distinguishable entries. The snapshots
// MUST be cleanly separated — no entry from goroutine A ever ends up
// in goroutine B's buffer.
//
// Pre-fix, this kind of cross-contamination was the empirically-observed
// symptom: push-fanout's invocation record contained log lines from
// rpc-router because both shared the singleton h.logs slice. This test
// codifies the invariant that with per-invocation buffers, that class
// of cross-talk is impossible.
func TestLogBuffer_concurrentInvocationsDoNotCrossContaminate(t *testing.T) {
	const (
		goroutines = 16
		opsPerG    = 50
	)
	var (
		wg       sync.WaitGroup
		failures int64
	)
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(gid int) {
			defer wg.Done()
			// Each goroutine simulates one invocation: fresh buffer +
			// fresh ctx, writes its own ID into each entry.
			buf := NewLogBuffer()
			ctx := WithLogBuffer(context.Background(), buf)
			myID := goroutineMarker(gid)

			for op := 0; op < opsPerG; op++ {
				// Pull buffer from ctx (mimics what host.LogInfo does)
				// and append. If a different goroutine's buffer somehow
				// got attached to this ctx, the entries land in the
				// wrong buffer and we detect it post-hoc.
				cur := LogBufferFromCtx(ctx)
				if cur != buf {
					atomic.AddInt64(&failures, 1)
					t.Errorf("goroutine %d: LogBufferFromCtx returned a different buffer", gid)
					return
				}
				cur.Append(LogEntry{Level: "info", Message: myID})
			}

			// Verify the snapshot is entirely this goroutine's entries —
			// no cross-talk. (Length AND content check.)
			snap := buf.Snapshot()
			if len(snap) != opsPerG {
				atomic.AddInt64(&failures, 1)
				t.Errorf("goroutine %d: snapshot len = %d; want %d (cross-contamination)",
					gid, len(snap), opsPerG)
				return
			}
			for _, e := range snap {
				if e.Message != myID {
					atomic.AddInt64(&failures, 1)
					t.Errorf("goroutine %d: snapshot contains foreign entry %q (want all %q)",
						gid, e.Message, myID)
					return
				}
			}
		}(g)
	}
	wg.Wait()

	if atomic.LoadInt64(&failures) != 0 {
		t.Fatalf("%d cross-contamination failures across %d concurrent invocations",
			atomic.LoadInt64(&failures), goroutines)
	}
}

// goroutineMarker is a deterministic per-goroutine message that
// uniquely identifies which goroutine wrote a log entry. Used by the
// cross-contamination test to verify the entry came from the right
// invocation.
func goroutineMarker(g int) string {
	return "goroutine-" + itoaLB(g)
}

// itoaLB avoids strconv to keep the test file's deps minimal.
func itoaLB(n int) string {
	if n == 0 {
		return "0"
	}
	digits := []byte{}
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}
