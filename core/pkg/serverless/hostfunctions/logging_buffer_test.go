package hostfunctions

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/serverless"
	"go.uber.org/zap"
)

// The regression guard for bugboard #108. A log line belongs to the invocation
// that wrote it, so it goes to that invocation's own buffer.
//
// It used to go to a slice on the shared HostFunctions, which is how
// push-fanout's invocation record came to contain rpc-router's log lines.
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

}

// A log call outside any invocation goes to the gateway's own log and to no
// invocation record, because there is no invocation for it to belong to.
//
// There used to be a fallback to a slice shared by every invocation on the
// gateway. It existed for callers that had not migrated, and it was the thing
// bugboard #108 was about.
func TestLogInfo_outsideAnInvocationBelongsToNoRecord(t *testing.T) {
	h := &HostFunctions{logger: zap.NewNop()}

	// No buffer on ctx: nothing to write into, and nothing shared to leak into
	// the next invocation that does have one.
	h.LogInfo(context.Background(), "outside any invocation")
	h.LogError(context.Background(), "also outside")

	buf := serverless.NewLogBuffer()
	h.LogInfo(serverless.WithLogBuffer(context.Background(), buf), "inside one")

	snap := buf.Snapshot()
	if len(snap) != 1 || snap[0].Message != "inside one" {
		t.Errorf("an invocation's record picked up lines written outside it: %+v", snap)
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
// Run with -race for stronger signal. Before the fix every goroutine wrote into
// one shared slice, and a different goroutine's snapshot scooped them up.
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
