package serverless

import (
	"context"
	"sync"
)

// logBufferKey is the unexported context-value key used to attach a
// per-invocation LogBuffer. Empty struct = standard Go pattern for ctx
// keys (avoids string-collision risk). Parallels invCtxKey used by
// WithInvocationContext — both fix the same class of singleton-state
// cross-contamination bug.
type logBufferKey struct{}

// LogBuffer collects WASM-emitted log entries (oh.LogInfo / oh.LogError)
// for ONE invocation. Each Engine.Execute creates a fresh LogBuffer and
// attaches it to the ctx passed to wazero; host functions extract it
// from ctx and append. Engine.logInvocation reads the buffer's snapshot
// when writing the invocation record.
//
// Why this exists: HostFunctions used to hold a singleton `logs` slice
// shared across every concurrent WASM invocation, with a per-call reset
// in SetInvocationContext. Two invocations executing concurrently would
// see each other's logs scooped up by whichever called GetLogs() first
// — empirically observed on bugboard #108 (push-fanout's invocation
// record contained rpc-router and message-push-handler log lines).
//
// The fix attaches a fresh LogBuffer to ctx per invocation. HostFunctions.
// LogInfo / LogError read the buffer from ctx and append to its
// invocation-local slice. The singleton h.logs field is kept as a
// back-compat fallback for tests that haven't been migrated, but no
// production code path relies on it once Engine.Execute is routing
// through the ctx buffer.
type LogBuffer struct {
	mu      sync.Mutex
	entries []LogEntry
}

// NewLogBuffer returns an empty buffer ready to receive entries.
func NewLogBuffer() *LogBuffer {
	return &LogBuffer{}
}

// maxLogEntriesPerInvocation caps how many log lines one invocation can
// buffer. Telemetry is best-effort; without a cap a tenant function looping
// oh.LogInfo could balloon gateway memory — amplified now that records sit
// in the async invocation-log queue (up to invocationLogQueueSize records
// resident) instead of being written and freed synchronously.
const maxLogEntriesPerInvocation = 1000

// Append adds one log entry, dropping silently once the per-invocation cap
// is reached (telemetry best-effort; bounds memory against log floods).
// Thread-safe — wazero modules aren't goroutine-safe in practice, but the
// lock makes the invariant explicit rather than relying on call-site
// discipline.
func (b *LogBuffer) Append(entry LogEntry) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.entries) >= maxLogEntriesPerInvocation {
		return
	}
	b.entries = append(b.entries, entry)
}

// Snapshot returns a defensive copy of the buffer's entries. Callers
// (e.g. Engine.logInvocation) iterate the snapshot without holding the
// buffer's lock.
func (b *LogBuffer) Snapshot() []LogEntry {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]LogEntry, len(b.entries))
	copy(out, b.entries)
	return out
}

// Len returns the number of buffered entries — used in tests to assert
// per-invocation accounting without making a full copy.
func (b *LogBuffer) Len() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.entries)
}

// WithLogBuffer returns a derived ctx that carries buf. HostFunctions.
// LogInfo / LogError check ctx FIRST and only fall back to the
// HostFunctions singleton slice if no buffer is attached.
//
// Callers MUST create a fresh LogBuffer per invocation (NewLogBuffer)
// rather than reusing one across calls — that's the whole point of the
// fix. Reusing a buffer would re-create the cross-contamination class.
func WithLogBuffer(ctx context.Context, buf *LogBuffer) context.Context {
	if buf == nil {
		return ctx
	}
	return context.WithValue(ctx, logBufferKey{}, buf)
}

// LogBufferFromCtx extracts the LogBuffer attached via WithLogBuffer, or
// nil if none is present (in which case callers fall back to the legacy
// singleton h.logs path). Exported so hostfunctions can retrieve the
// buffer without re-importing the key type.
func LogBufferFromCtx(ctx context.Context) *LogBuffer {
	if ctx == nil {
		return nil
	}
	v, _ := ctx.Value(logBufferKey{}).(*LogBuffer)
	return v
}
