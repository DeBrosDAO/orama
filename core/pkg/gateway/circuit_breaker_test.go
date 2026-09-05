package gateway

import (
	"testing"
	"time"
)

func openedBreaker(t *testing.T) *CircuitBreaker {
	t.Helper()
	cb := NewCircuitBreaker()
	for i := 0; i < defaultFailureThreshold; i++ {
		cb.RecordFailure()
	}
	if cb.State() != CircuitOpen {
		t.Fatalf("state = %v, want open after %d failures", cb.State(), defaultFailureThreshold)
	}
	return cb
}

func TestBreakerOpensOnlyAtTheThreshold(t *testing.T) {
	cb := NewCircuitBreaker()
	for i := 0; i < defaultFailureThreshold-1; i++ {
		cb.RecordFailure()
		if cb.State() != CircuitClosed {
			t.Fatalf("opened after %d failures, threshold is %d", i+1, defaultFailureThreshold)
		}
	}
	cb.RecordFailure()
	if cb.State() != CircuitOpen {
		t.Errorf("state = %v, want open", cb.State())
	}
	if cb.Allow() {
		t.Error("an open breaker admitted a request before its open duration elapsed")
	}
}

// The regression this ticket is about. Allow() admits exactly one probe and
// refuses everyone else until an outcome is recorded, so a caller that never
// reports used to remove the target from rotation for the life of the process.
func TestHalfOpenProbeThatNeverReportsReopens(t *testing.T) {
	cb := openedBreaker(t)
	cb.openDuration = 0
	cb.halfOpenTimeout = 20 * time.Millisecond

	if !cb.Allow() {
		t.Fatal("breaker did not admit a probe after its open duration")
	}
	if cb.State() != CircuitHalfOpen {
		t.Fatalf("state = %v, want half-open", cb.State())
	}
	// A second caller is refused while the probe is in flight.
	if cb.Allow() {
		t.Error("a second caller was admitted while a probe was in flight")
	}

	time.Sleep(40 * time.Millisecond)

	// The probe never reported. The breaker must fall back to open rather than
	// hold the slot, and then become probeable again.
	if cb.Allow() {
		t.Error("the abandoned probe slot admitted a caller instead of re-opening")
	}
	if cb.State() != CircuitOpen {
		t.Errorf("state = %v, want open after the probe timed out", cb.State())
	}
	if !cb.Allow() {
		t.Error("breaker did not admit a fresh probe after re-opening")
	}
}

func TestHalfOpenSuccessCloses(t *testing.T) {
	cb := openedBreaker(t)
	cb.openDuration = 0

	if !cb.Allow() {
		t.Fatal("no probe admitted")
	}
	cb.RecordSuccess()
	if cb.State() != CircuitClosed {
		t.Errorf("state = %v, want closed after a successful probe", cb.State())
	}
	if !cb.Allow() {
		t.Error("a closed breaker refused a request")
	}
}

// A failed probe has just proven the target is still sick; sending it four more
// doomed requests before re-opening helps nobody.
func TestHalfOpenFailureReopensImmediately(t *testing.T) {
	cb := openedBreaker(t)
	cb.openDuration = 0

	if !cb.Allow() {
		t.Fatal("no probe admitted")
	}
	cb.RecordSuccess() // reset to closed, failures = 0
	cb.RecordFailure() // a single failure from closed must NOT open
	if cb.State() != CircuitClosed {
		t.Fatalf("state = %v, want closed after one failure", cb.State())
	}

	cb2 := openedBreaker(t)
	cb2.openDuration = 0
	if !cb2.Allow() {
		t.Fatal("no probe admitted")
	}
	cb2.RecordFailure()
	if cb2.State() != CircuitOpen {
		t.Errorf("state = %v, want open immediately after a failed probe", cb2.State())
	}
}

// The registry is keyed by target IP and never shed entries: every node ever
// removed from the cluster stayed in the map for the life of the process.
func TestRegistryPrunesIdleBreakers(t *testing.T) {
	r := NewCircuitBreakerRegistry()
	stale := r.Get("ns:10.0.0.9")
	fresh := r.Get("ns:10.0.0.1")

	stale.mu.Lock()
	stale.lastUsed = time.Now().Add(-time.Hour)
	stale.mu.Unlock()
	_ = fresh.Allow()

	if dropped := r.Prune(30 * time.Minute); dropped != 1 {
		t.Errorf("pruned %d breakers, want 1", dropped)
	}
	snap := r.Snapshot()
	if _, ok := snap["ns:10.0.0.9"]; ok {
		t.Error("the idle breaker survived the prune")
	}
	if _, ok := snap["ns:10.0.0.1"]; !ok {
		t.Error("the active breaker was pruned")
	}
}

// A breaker fast-failing a sick target must not be mistaken for an idle one.
func TestPruneKeepsRecentlyUsedOpenBreakers(t *testing.T) {
	r := NewCircuitBreakerRegistry()
	cb := r.Get("ns:10.0.0.2")
	for i := 0; i < defaultFailureThreshold; i++ {
		cb.RecordFailure()
	}
	if dropped := r.Prune(30 * time.Minute); dropped != 0 {
		t.Errorf("pruned %d breakers, want 0", dropped)
	}
	if got := r.Snapshot()["ns:10.0.0.2"]; got != "open" {
		t.Errorf("snapshot state = %q, want open", got)
	}
}
