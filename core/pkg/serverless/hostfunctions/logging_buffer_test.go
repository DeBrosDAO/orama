package hostfunctions

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/serverless"
	"go.uber.org/zap"
)

// TestLogInfo_writesToCtxBuffer is the regression guard for bugboard
// #108. When the caller attaches a per-invocation LogBuffer to ctx,
// LogInfo MUST write to that buffer (not to the singleton h.logs).
//
// Pre-fix, LogInfo always wrote to h.logs, causing cross-contamination
// between concurrent invocations.
func TestLogInfo_writesToCtxBuffer(t *testing.T) {
	h := &HostFunctions{logger: zap.NewNop()}
	buf := serverless.NewLogBuffer()
	ctx := serverless.WithLogBuffer(context.Background(), buf)

	h.LogInfo(ctx, "hello from invocation A")
	h.LogError(ctx, "boom from invocation A")

	snap := buf.Snapshot()
	if len(snap) != 2 {
		t.Fatalf("ctx buffer len = %d; want 2", len(snap))
	}
	if snap[0].Level != "info" || snap[0].Message != "hello from invocation A" {
		t.Errorf("info entry wrong: %+v", snap[0])
	}
	if snap[1].Level != "error" || snap[1].Message != "boom from invocation A" {
		t.Errorf("error entry wrong: %+v", snap[1])
	}

	// The singleton must NOT have been touched.
	if len(h.logs) != 0 {
		t.Errorf("singleton h.logs got %d entries; want 0 (ctx buffer should have absorbed them)",
			len(h.logs))
	}
}

// TestLogInfo_fallsBackToSingletonWhenNoBuffer preserves the legacy
// behavior for callers (tests, mostly) that haven't migrated to the
// ctx-attached buffer path yet. Without this fallback, every test that
// constructed a HostFunctions directly and called LogInfo without
// wrapping ctx would silently lose log entries.
func TestLogInfo_fallsBackToSingletonWhenNoBuffer(t *testing.T) {
	h := &HostFunctions{logger: zap.NewNop()}
	// No buffer attached to ctx.
	h.LogInfo(context.Background(), "legacy call")
	h.LogError(context.Background(), "legacy error")

	if len(h.logs) != 2 {
		t.Errorf("singleton h.logs got %d entries; want 2 (legacy fallback)", len(h.logs))
	}
}

// TestLogInfo_concurrentInvocations_noCrossContamination is THE
// regression guard for bugboard #108's empirically-observed symptom:
// push-fanout's invocation record contained log lines from rpc-router
// because both shared the singleton h.logs slice.
//
// Sixteen goroutines simulating concurrent invocations each attach
// their own LogBuffer to ctx, then write distinguishable entries via
// HostFunctions.LogInfo. After all goroutines complete, each buffer
// must contain ONLY its own entries — zero cross-talk.
//
// Run with -race for stronger signal. Pre-fix (singleton h.logs), every
// goroutine wrote into the shared slice and a different goroutine's
// GetLogs() snapshot would scoop them up.
func TestLogInfo_concurrentInvocations_noCrossContamination(t *testing.T) {
	h := &HostFunctions{logger: zap.NewNop()}

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
			buf := serverless.NewLogBuffer()
			ctx := serverless.WithLogBuffer(context.Background(), buf)
			myMarker := workloadMarker(gid)

			for op := 0; op < opsPerG; op++ {
				h.LogInfo(ctx, myMarker)
			}

			snap := buf.Snapshot()
			if len(snap) != opsPerG {
				atomic.AddInt64(&failures, 1)
				t.Errorf("goroutine %d: snapshot len = %d; want %d", gid, len(snap), opsPerG)
				return
			}
			for _, e := range snap {
				if e.Message != myMarker {
					atomic.AddInt64(&failures, 1)
					t.Errorf("goroutine %d: foreign entry %q in own buffer", gid, e.Message)
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

	// Singleton must NOT have grown — every write went to a ctx buffer.
	if len(h.logs) != 0 {
		t.Errorf("singleton h.logs got %d entries; want 0 (all should have gone to ctx buffers)",
			len(h.logs))
	}
}

func workloadMarker(g int) string {
	return "workload-" + itoaHF(g)
}

func itoaHF(n int) string {
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
