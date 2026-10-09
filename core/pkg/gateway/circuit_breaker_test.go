package gateway

import (
	"fmt"
	"sync"
	"testing"
	"time"
)

func openedBreaker(t *testing.T) *CircuitBreaker {
	t.Helper()
	cb := NewCircuitBreaker()
	for i := 0; i < defaultFailureThreshold; i++ {
		cb.RecordFailure("test")
	}
	if cb.State() != CircuitOpen {
		t.Fatalf("state = %v, want open after %d failures", cb.State(), defaultFailureThreshold)
	}
	return cb
}

func TestBreakerOpensOnlyAtTheThreshold(t *testing.T) {
	cb := NewCircuitBreaker()
	for i := 0; i < defaultFailureThreshold-1; i++ {
		cb.RecordFailure("test")
		if cb.State() != CircuitClosed {
			t.Fatalf("opened after %d failures, threshold is %d", i+1, defaultFailureThreshold)
		}
	}
	cb.RecordFailure("test")
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
	cb.RecordSuccess()       // reset to closed, failures = 0
	cb.RecordFailure("test") // a single failure from closed must NOT open
	if cb.State() != CircuitClosed {
		t.Fatalf("state = %v, want closed after one failure", cb.State())
	}

	cb2 := openedBreaker(t)
	cb2.openDuration = 0
	if !cb2.Allow() {
		t.Fatal("no probe admitted")
	}
	cb2.RecordFailure("test")
	if cb2.State() != CircuitOpen {
		t.Errorf("state = %v, want open immediately after a failed probe", cb2.State())
	}
}

// The registry never shed entries: every namespace gateway ever proxied stayed
// in the map for the life of the process.
func TestRegistryPrunesIdleBreakers(t *testing.T) {
	r := NewCircuitBreakerRegistry()
	stale := r.ForNamespaceGateway("acme", "10.0.0.9")
	fresh := r.ForNamespaceGateway("acme", "10.0.0.1")

	stale.mu.Lock()
	stale.lastUsed = time.Now().Add(-time.Hour)
	stale.mu.Unlock()
	_ = fresh.Allow()

	if dropped := r.Prune(30 * time.Minute); dropped != 1 {
		t.Errorf("pruned %d breakers, want 1", dropped)
	}
	if r.Len() != 1 {
		t.Errorf("registry holds %d breakers, want 1", r.Len())
	}
	if r.ForNamespaceGateway("acme", "10.0.0.1") != fresh {
		t.Error("the active breaker was pruned")
	}
}

// A breaker fast-failing a sick target must not be mistaken for an idle one.
func TestPruneKeepsRecentlyUsedOpenBreakers(t *testing.T) {
	r := NewCircuitBreakerRegistry()
	cb := r.ForNamespaceGateway("acme", "10.0.0.2")
	for i := 0; i < defaultFailureThreshold; i++ {
		cb.RecordFailure("test")
	}
	if dropped := r.Prune(30 * time.Minute); dropped != 0 {
		t.Errorf("pruned %d breakers, want 0", dropped)
	}
	got := r.Unhealthy(breakerReportIdle)
	if len(got) != 1 || got[0].State != CircuitOpen || got[0].Namespace != "acme" || got[0].Node != "10.0.0.2" {
		t.Errorf("unhealthy = %+v, want acme@10.0.0.2 open", got)
	}
}

// collectTransitions registers an observer on r and returns where the
// transitions it is told about are gathered.
func collectTransitions(r *CircuitBreakerRegistry) *[]BreakerTransition {
	var (
		mu  sync.Mutex
		got []BreakerTransition
	)
	r.SetObserver(func(tr BreakerTransition) {
		mu.Lock()
		got = append(got, tr)
		mu.Unlock()
	})
	return &got
}

// One breaker per namespace gateway: two namespaces on the same node, and one
// namespace on two nodes, never share a breaker.
func TestNamespaceBreakerKey_namesTheNamespaceAndTheNode(t *testing.T) {
	r := NewCircuitBreakerRegistry()
	a := r.ForNamespaceGateway("acme", "10.0.0.1")
	if r.ForNamespaceGateway("acme", "10.0.0.1") != a {
		t.Error("the same gateway got two breakers")
	}
	if r.ForNamespaceGateway("beta", "10.0.0.1") == a {
		t.Error("two namespaces on one node share a breaker")
	}
	if r.ForNamespaceGateway("acme", "10.0.0.2") == a {
		t.Error("one namespace on two nodes shares a breaker")
	}
}

func TestBreakerTransitions_areReportedWithTheirContext(t *testing.T) {
	r := NewCircuitBreakerRegistry()
	got := collectTransitions(r)
	cb := r.ForNamespaceGateway("acme", "10.0.0.7")
	cb.openDuration = 0

	for i := 0; i < defaultFailureThreshold; i++ {
		cb.RecordFailure("connection refused")
	}
	cb.Allow()                     // open -> half-open
	cb.RecordFailure("still down") // half-open -> open
	cb.Allow()                     // open -> half-open
	cb.RecordSuccess()             // half-open -> closed

	want := []struct {
		from, to CircuitState
		failures int
		lastErr  string
	}{
		{CircuitClosed, CircuitOpen, defaultFailureThreshold, "connection refused"},
		{CircuitOpen, CircuitHalfOpen, defaultFailureThreshold, "connection refused"},
		{CircuitHalfOpen, CircuitOpen, defaultFailureThreshold + 1, "still down"},
		// The second probe cycle is inside breakerCycleLogInterval: its
		// half-open is left out, and the close says so.
		{CircuitHalfOpen, CircuitClosed, defaultFailureThreshold + 1, "still down"},
	}
	if len(*got) != len(want) {
		t.Fatalf("%d transitions reported, want %d: %+v", len(*got), len(want), *got)
	}
	for i, w := range want {
		g := (*got)[i]
		if g.From != w.from || g.To != w.to || g.Failures != w.failures || g.LastError != w.lastErr ||
			g.Namespace != "acme" || g.Node != "10.0.0.7" || g.Key != namespaceBreakerKey("acme", "10.0.0.7") {
			t.Errorf("transition %d = %+v, want %v -> %v after %d failures (%q) for acme@10.0.0.7",
				i, g, w.from, w.to, w.failures, w.lastErr)
		}
	}
}

// A dead node keeps every breaker toward it cycling open, half-open, open;
// one line per probe per breaker would drown the log. The opening, one cycle,
// and the closing (with how many cycles it left out) are what it says.
func TestBreakerCycles_areRateLimited(t *testing.T) {
	r := NewCircuitBreakerRegistry()
	got := collectTransitions(r)
	cb := r.ForNamespaceGateway("acme", "10.0.0.7")
	cb.openDuration = 0
	for i := 0; i < defaultFailureThreshold; i++ {
		cb.RecordFailure("connection refused")
	}
	const cycles = 20
	for i := 0; i < cycles; i++ {
		if !cb.Allow() {
			t.Fatal("no probe admitted")
		}
		cb.RecordFailure("connection refused")
	}
	// closed->open, then the first cycle's two lines.
	if len(*got) != 3 {
		t.Fatalf("%d transitions reported for %d probe cycles, want 3: %+v", len(*got), cycles, *got)
	}
	cb.Allow()
	cb.RecordSuccess()
	last := (*got)[len(*got)-1]
	if last.To != CircuitClosed || last.SuppressedCycles != cycles {
		t.Errorf("close = %+v, want it to report %d suppressed cycles", last, cycles)
	}
}

// A request that ends without telling anything about the target must give back
// the half-open probe slot, or the next caller waits out halfOpenTimeout.
func TestAbandon_releasesTheProbeSlot(t *testing.T) {
	cb := openedBreaker(t)
	cb.openDuration = 0
	if !cb.Allow() {
		t.Fatal("no probe admitted")
	}
	if cb.Allow() {
		t.Fatal("a second probe was admitted while the first was in flight")
	}
	cb.Abandon()
	if cb.State() != CircuitOpen {
		t.Fatalf("state = %v, want open after the probe was abandoned", cb.State())
	}
	if !cb.Allow() {
		t.Error("the next caller was not admitted as the probe")
	}
}

func TestAbandon_changesNothingOutsideHalfOpen(t *testing.T) {
	cb := NewCircuitBreaker()
	cb.Abandon()
	if cb.State() != CircuitClosed || !cb.Allow() {
		t.Error("abandoning on a closed breaker changed it")
	}
	open := openedBreaker(t)
	open.Abandon()
	if open.State() != CircuitOpen || open.Allow() {
		t.Error("abandoning on an open breaker changed it")
	}
}

// The registry holds one breaker per gateway that exists: the member list the
// registry returns is the whole truth about which breakers may stay.
func TestRetainNamespaceMembers_dropsWhatIsNoLongerAMember(t *testing.T) {
	r := NewCircuitBreakerRegistry()
	keep := r.ForNamespaceGateway("acme", "10.0.0.1")
	r.ForNamespaceGateway("acme", "10.0.0.2")
	other := r.ForNamespaceGateway("beta", "10.0.0.2")
	nodeScoped := r.Get("node:10.0.0.2")

	if dropped := r.RetainNamespaceMembers("acme", []string{"10.0.0.1"}); dropped != 1 {
		t.Fatalf("dropped %d, want 1 (acme on 10.0.0.2)", dropped)
	}
	if r.ForNamespaceGateway("acme", "10.0.0.1") != keep ||
		r.ForNamespaceGateway("beta", "10.0.0.2") != other ||
		r.Get("node:10.0.0.2") != nodeScoped {
		t.Error("a breaker that was not acme's departed member was dropped")
	}
	if dropped := r.RetainNamespaceMembers("acme", nil); dropped != 1 {
		t.Errorf("dropped %d when acme was removed, want its last breaker", dropped)
	}
	if r.Len() != 2 {
		t.Errorf("registry holds %d breakers, want beta's and the node's", r.Len())
	}
}

// Namespaces x nodes breakers is the registry's size at most, and namespaces
// that are removed give theirs back: the registry does not grow with the
// namespaces that ever existed.
func TestRegistry_isBoundedByTheNamespacesThatExist(t *testing.T) {
	r := NewCircuitBreakerRegistry()
	const namespaces, nodes = 200, 3
	for n := 0; n < namespaces; n++ {
		for m := 0; m < nodes; m++ {
			r.ForNamespaceGateway(fmt.Sprintf("ns%d", n), fmt.Sprintf("10.0.0.%d", m+1))
		}
	}
	if r.Len() != namespaces*nodes {
		t.Fatalf("registry holds %d breakers, want %d", r.Len(), namespaces*nodes)
	}
	for n := 10; n < namespaces; n++ {
		r.RetainNamespaceMembers(fmt.Sprintf("ns%d", n), nil)
	}
	if r.Len() != 10*nodes {
		t.Errorf("registry holds %d breakers after 190 namespaces were removed, want %d", r.Len(), 10*nodes)
	}
}

func TestUnhealthy_listsTheBreakersThatAreNotClosed(t *testing.T) {
	r := NewCircuitBreakerRegistry()
	r.ForNamespaceGateway("zeta", "10.0.0.1")
	for _, ns := range []string{"beta", "acme"} {
		cb := r.ForNamespaceGateway(ns, "10.0.0.2")
		for i := 0; i < defaultFailureThreshold; i++ {
			cb.RecordFailure("connection refused")
		}
	}
	got := r.Unhealthy(breakerReportIdle)
	if len(got) != 2 || got[0].Namespace != "acme" || got[1].Namespace != "beta" ||
		got[0].State != CircuitOpen || got[0].LastError != "connection refused" || got[0].Failures != defaultFailureThreshold {
		t.Fatalf("unhealthy = %+v, want acme then beta, open, with their reason", got)
	}
}

// An open breaker whose namespace nobody asks about refuses no one: reporting
// it would raise an alert for a deleted namespace until the prune.
func TestUnhealthy_leavesOutABreakerNobodyAsksAbout(t *testing.T) {
	r := NewCircuitBreakerRegistry()
	stale := r.ForNamespaceGateway("gone", "10.0.0.2")
	live := r.ForNamespaceGateway("acme", "10.0.0.2")
	for _, cb := range []*CircuitBreaker{stale, live} {
		for i := 0; i < defaultFailureThreshold; i++ {
			cb.RecordFailure("connection refused")
		}
	}
	stale.mu.Lock()
	stale.lastUsed = time.Now().Add(-time.Hour)
	stale.mu.Unlock()

	got := r.Unhealthy(5 * time.Minute)
	if len(got) != 1 || got[0].Namespace != "acme" {
		t.Fatalf("unhealthy = %+v, want only acme's", got)
	}
}
