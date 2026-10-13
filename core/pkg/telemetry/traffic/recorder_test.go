package traffic

import (
	"fmt"
	"math"
	"net/http"
	"sync"
	"testing"
	"time"
)

// fakeClock is a settable clock for deterministic windows.
type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func newFakeClock() *fakeClock {
	return &fakeClock{t: time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)}
}

func (c *fakeClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

func assertFloat(t *testing.T, name string, got, want float64) {
	t.Helper()
	if math.Abs(got-want) > floatTolerance {
		t.Errorf("%s = %v, want %v", name, got, want)
	}
}

func TestSnapshot_emptyRecorder(t *testing.T) {
	clock := newFakeClock()
	r := New(clock.now)
	got := r.Snapshot(clock.now())
	if got.WindowSec != 0 || got.Requests != 0 || got.TotalRequests != 0 || got.Namespaces != nil {
		t.Fatalf("empty snapshot = %+v, want zero value", got)
	}
	assertFloat(t, "rps", got.RPS, 0)
	assertFloat(t, "error rate", got.ErrorRate, 0)
	assertFloat(t, "p99", got.P99Ms, 0)
}

func TestNew_nilClockUsesTimeNow(t *testing.T) {
	r := New(nil)
	r.Observe("anchat", http.StatusOK, 1, time.Millisecond)
	if got := r.Snapshot(time.Now()); got.Requests != 1 {
		t.Fatalf("requests = %d, want 1", got.Requests)
	}
}

func TestSnapshot_errorRateAndCounts(t *testing.T) {
	clock := newFakeClock()
	r := New(clock.now)
	statuses := []int{200, 201, 301, 400, 404, 429, 500, 502, 503, 200}
	for _, s := range statuses {
		r.Observe("anchat", s, 100, 10*time.Millisecond)
	}
	got := r.Snapshot(clock.now())
	if got.Requests != 10 || got.Errors4xx != 3 || got.Errors5xx != 3 {
		t.Fatalf("requests/4xx/5xx = %d/%d/%d, want 10/3/3", got.Requests, got.Errors4xx, got.Errors5xx)
	}
	assertFloat(t, "error rate", got.ErrorRate, 0.3)
	if len(got.Namespaces) != 1 || got.Namespaces[0].Errors5xx != 3 {
		t.Fatalf("namespaces = %+v, want anchat with 3 5xx", got.Namespaces)
	}
}

func TestSnapshot_uptimeShorterThanWindow(t *testing.T) {
	clock := newFakeClock()
	r := New(clock.now)
	for range 20 {
		r.Observe("anchat", http.StatusOK, 50, time.Millisecond)
		clock.advance(time.Second / 2)
	}
	// First request 10s ago: rates cover 10s, not the whole window.
	got := r.Snapshot(clock.now())
	if got.WindowSec != 10 {
		t.Fatalf("window = %d, want 10", got.WindowSec)
	}
	assertFloat(t, "rps", got.RPS, 2)
	assertFloat(t, "bytes/s", got.BytesPerSec, 100)
	assertFloat(t, "namespace rps", got.Namespaces[0].RPS, 2)
}

func TestSnapshot_coversAtLeastOneSecond(t *testing.T) {
	clock := newFakeClock()
	r := New(clock.now)
	r.Observe("anchat", http.StatusOK, 0, time.Millisecond)
	r.Observe("anchat", http.StatusOK, 0, time.Millisecond)
	got := r.Snapshot(clock.now())
	if got.WindowSec != 1 {
		t.Fatalf("window = %d, want 1", got.WindowSec)
	}
	assertFloat(t, "rps", got.RPS, 2)
}

func TestSnapshot_windowRollover(t *testing.T) {
	clock := newFakeClock()
	r := New(clock.now)
	r.Observe("old", http.StatusInternalServerError, 0, time.Millisecond)
	clock.advance(30 * time.Second)
	r.Observe("new", http.StatusOK, 0, time.Millisecond)
	clock.advance(31 * time.Second)

	got := r.Snapshot(clock.now())
	if got.Requests != 1 || got.Errors5xx != 0 {
		t.Fatalf("requests/5xx = %d/%d, want only the newer request", got.Requests, got.Errors5xx)
	}
	if got.WindowSec != WindowSeconds {
		t.Fatalf("window = %d, want %d", got.WindowSec, WindowSeconds)
	}
	if got.TotalRequests != 2 {
		t.Fatalf("total = %d, want 2 (monotonic since start)", got.TotalRequests)
	}
	if len(got.Namespaces) != 1 || got.Namespaces[0].Namespace != "new" {
		t.Fatalf("namespaces = %+v, want only new", got.Namespaces)
	}
}

func TestSnapshot_idleGapLongerThanWindow(t *testing.T) {
	clock := newFakeClock()
	r := New(clock.now)
	for range 5 {
		r.Observe("anchat", http.StatusBadGateway, 0, time.Second)
	}
	clock.advance(2 * WindowSeconds * time.Second)

	if got := r.Snapshot(clock.now()); got.Requests != 0 || got.Namespaces != nil {
		t.Fatalf("after idle gap = %+v, want an empty window", got)
	}

	// The next request reuses a ring bucket that still holds stale counts.
	r.Observe("anchat", http.StatusOK, 0, time.Millisecond)
	got := r.Snapshot(clock.now())
	if got.Requests != 1 || got.Errors5xx != 0 || got.TotalRequests != 6 {
		t.Fatalf("after reuse = %+v, want 1 fresh request of 6 total", got)
	}
	assertFloat(t, "p99", got.P99Ms, 0.99)
}

func TestObserve_sameRingSlotNextLap(t *testing.T) {
	clock := newFakeClock()
	r := New(clock.now)
	r.Observe("anchat", http.StatusOK, 0, time.Millisecond)
	clock.advance(WindowSeconds * time.Second)
	r.Observe("anchat", http.StatusOK, 0, time.Millisecond)
	if got := r.Snapshot(clock.now()); got.Requests != 1 {
		t.Fatalf("requests = %d, want 1 (the lap-old second was reset)", got.Requests)
	}
}

func TestObserve_namespaceCapFoldsIntoOther(t *testing.T) {
	clock := newFakeClock()
	r := New(clock.now)
	const extra = 50
	for i := range MaxNamespacesPerSecond + extra {
		r.Observe(fmt.Sprintf("ns%d", i), http.StatusOK, 0, time.Millisecond)
	}
	// A namespace tracked before the cap keeps its own label.
	r.Observe("ns0", http.StatusOK, 0, time.Millisecond)

	b := &r.ring[r.secondAt(clock.now())%WindowSeconds]
	if len(b.namespaces) != MaxNamespacesPerSecond+1 {
		t.Fatalf("tracked labels = %d, want %d", len(b.namespaces), MaxNamespacesPerSecond+1)
	}
	got := r.Snapshot(clock.now())
	if len(got.Namespaces) != TopNamespaces {
		t.Fatalf("listed namespaces = %d, want %d", len(got.Namespaces), TopNamespaces)
	}
	if got.Namespaces[0].Namespace != OtherLabel || got.Namespaces[0].Requests != extra {
		t.Fatalf("busiest = %+v, want %s with %d", got.Namespaces[0], OtherLabel, extra)
	}
	if got.Namespaces[1].Namespace != "ns0" || got.Namespaces[1].Requests != 2 {
		t.Fatalf("second = %+v, want ns0 with 2", got.Namespaces[1])
	}
}

func TestObserve_invalidNamespaceLabel(t *testing.T) {
	clock := newFakeClock()
	r := New(clock.now)
	r.Observe("../../etc", http.StatusNotFound, 0, time.Millisecond)
	r.Observe("", http.StatusNotFound, 0, time.Millisecond)
	got := r.Snapshot(clock.now())
	if len(got.Namespaces) != 1 || got.Namespaces[0].Namespace != InvalidLabel || got.Namespaces[0].Requests != 2 {
		t.Fatalf("namespaces = %+v, want both under %s", got.Namespaces, InvalidLabel)
	}
}

func TestObserveStream_countsWithoutLatency(t *testing.T) {
	clock := newFakeClock()
	r := New(clock.now)
	r.Observe("anchat", http.StatusOK, 0, 4*time.Millisecond)
	r.ObserveStream("anchat", http.StatusSwitchingProtocols, 1024)
	got := r.Snapshot(clock.now())
	if got.Requests != 2 {
		t.Fatalf("requests = %d, want 2", got.Requests)
	}
	// Only the timed request is a sample, in bucket (3, 5].
	assertFloat(t, "p99", got.P99Ms, 3+0.99*2)
	assertFloat(t, "namespace p95", got.Namespaces[0].P95Ms, 3+0.95*2)
}

func TestSnapshot_timeBeforeLastObservation(t *testing.T) {
	clock := newFakeClock()
	r := New(clock.now)
	past := clock.now()
	clock.advance(5 * time.Second)
	r.Observe("anchat", http.StatusOK, 0, time.Millisecond)
	if got := r.Snapshot(past); got.Requests != 0 {
		t.Fatalf("requests = %d, want 0 for a window that ends before the request", got.Requests)
	}
}

func TestObserve_concurrent(t *testing.T) {
	clock := newFakeClock()
	r := New(clock.now)
	const workers, perWorker = 8, 500
	var wg sync.WaitGroup
	for w := range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range perWorker {
				r.Observe(fmt.Sprintf("ns%d", w), http.StatusOK, 1, time.Duration(i)*time.Microsecond)
				if i%100 == 0 {
					r.Snapshot(clock.now())
				}
			}
		}()
	}
	wg.Wait()
	got := r.Snapshot(clock.now())
	if got.Requests != workers*perWorker || got.TotalRequests != workers*perWorker {
		t.Fatalf("requests/total = %d/%d, want %d", got.Requests, got.TotalRequests, workers*perWorker)
	}
	if len(got.Namespaces) != workers {
		t.Fatalf("namespaces = %d, want %d", len(got.Namespaces), workers)
	}
}

func BenchmarkObserve(b *testing.B) {
	r := New(nil)
	for b.Loop() {
		r.Observe("anchat", http.StatusOK, 512, 3*time.Millisecond)
	}
}

func BenchmarkObserveParallel(b *testing.B) {
	r := New(nil)
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			r.Observe("anchat", http.StatusOK, 512, 3*time.Millisecond)
		}
	})
}
