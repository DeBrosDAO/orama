package serverless

import (
	"fmt"
	"testing"
)

// Security hardening (feat-27 async-logging review): one invocation cannot
// buffer unbounded log lines — the cap bounds gateway memory while records
// sit in the async invocation-log queue.
func TestLogBuffer_capsEntriesPerInvocation(t *testing.T) {
	b := NewLogBuffer()
	for i := 0; i < maxLogEntriesPerInvocation+500; i++ {
		b.Append(LogEntry{Level: "info", Message: fmt.Sprintf("line %d", i)})
	}
	if got := b.Len(); got != maxLogEntriesPerInvocation {
		t.Errorf("Len() = %d; want cap %d (excess lines must be dropped, not buffered)", got, maxLogEntriesPerInvocation)
	}
	// First entries are kept (drop-newest semantics).
	snap := b.Snapshot()
	if snap[0].Message != "line 0" {
		t.Errorf("first entry = %q; want \"line 0\" (cap drops newest, keeps earliest)", snap[0].Message)
	}
}
