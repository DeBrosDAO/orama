//go:build e2e_fleet

package tenancy

import (
	"sync"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/harness/fleet"
	"github.com/DeBrosOfficial/network/e2e/harness/ns"
)

// LiveNamespaces is how many namespaces one feature package holds at once.
//
// Every namespace takes a five-port block on every node, and a node has room
// for twenty (docs/ARCHITECTURE.md; core/pkg/namespace MaxNamespacesPerNode).
// A stage runs its packages in parallel and go test runs a package's tests in
// parallel, so without a bound three data-plane packages alone would ask for
// more namespaces than the fleet can host, and provisioning would fail for
// reasons that have nothing to do with the feature under test.
const LiveNamespaces = 5

var budget = newQuota(LiveNamespaces)

// quota is a counting semaphore that hands out several slots at once, so a
// test that needs two namespaces never holds one while waiting for the other.
type quota struct {
	mu   sync.Mutex
	cond *sync.Cond
	free int
}

func newQuota(n int) *quota {
	q := &quota{free: n}
	q.cond = sync.NewCond(&q.mu)
	return q
}

func (q *quota) acquire(n int) {
	q.mu.Lock()
	defer q.mu.Unlock()
	for q.free < n {
		q.cond.Wait()
	}
	q.free -= n
}

func (q *quota) release(n int) {
	q.mu.Lock()
	q.free += n
	q.mu.Unlock()
	q.cond.Broadcast()
}

// Namespaces creates count namespaces with opts (opts.Name must be empty
// when count > 1) once the package has room for
// all of them, and gives the room back after their teardown.
func Namespaces(t testing.TB, f *fleet.Fleet, count int, opts ns.Options) []*ns.Namespace {
	t.Helper()
	Reserve(t, f, count)
	out := make([]*ns.Namespace, count)
	for i := range out {
		out[i] = ns.New(t, f, opts)
	}
	return out
}

// Reserve takes room for count namespaces t creates (with ns.New, or with
// Create and Adopt, to assert on the creation or the deletion) and gives it
// back when t ends. It takes the package's room first, then the fleet-wide
// slots with ns.Hold, always in that order: every test waits on the package
// quota before it holds a fleet slot, so no test holds fleet slots while it
// waits for package room. The ns.New calls that follow use the held slots
// instead of taking one each; a Create/Adopt namespace is covered by them too.
// Call it before creating them, so the release runs after their teardown.
//
// Reserve in the test that creates the namespaces, never in a parent whose
// subtests call ns.New: a hold is per testing.TB, so a subtest's ns.New does
// not see its parent's slots and would count every namespace twice.
func Reserve(t testing.TB, f *fleet.Fleet, count int) {
	t.Helper()
	if count < 1 || count > LiveNamespaces {
		t.Fatalf("a test may hold 1 to %d namespaces, asked for %d", LiveNamespaces, count)
	}
	budget.acquire(count)
	t.Cleanup(func() { budget.release(count) })
	ns.Hold(t, f, count)
}

// Namespace is Namespaces for one.
func Namespace(t testing.TB, f *fleet.Fleet, opts ns.Options) *ns.Namespace {
	t.Helper()
	return Namespaces(t, f, 1, opts)[0]
}
