package ns

import (
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/harness/config"
	"github.com/DeBrosOfficial/network/e2e/harness/fleet"
)

// held tracks, per test, the slots Hold pre-reserved and New has not used
// yet (unused), and every slot the test holds (taken).
var held = struct {
	sync.Mutex
	unused map[testing.TB]int
	taken  map[testing.TB]int
}{unused: map[testing.TB]int{}, taken: map[testing.TB]int{}}

// HoldFirstMessage fails a test that would wait for a slot while holding one.
const HoldFirstMessage = "this test already holds a live-namespace slot and would wait for another while holding it " +
	"(a hold-and-wait deadlock once parallel tests fill the fleet): call ns.Hold(t, f, n) first with every namespace it creates"

// Hold reserves count fleet-wide live-namespace slots for t at once and
// releases them when t ends; the next count calls of New in t use them
// instead of taking one each. A test that creates several namespaces calls
// it first, so it never holds some slots while waiting for the others.
// Outside a fleet run it does nothing.
func Hold(t testing.TB, f *fleet.Fleet, count int) {
	t.Helper()
	failIfHolding(t)
	if takeFleetSlots(t, f, count) {
		held.Lock()
		held.unused[t] += count
		held.Unlock()
		t.Cleanup(func() {
			held.Lock()
			delete(held.unused, t)
			held.Unlock()
		})
	}
}

// fleetSlot gives New its slot: one Hold reserved for t, or a new one. The
// release is registered before New registers the namespace's deletion, so
// it runs after the teardown.
func fleetSlot(t testing.TB, f *fleet.Fleet) {
	t.Helper()
	held.Lock()
	if held.unused[t] > 0 {
		held.unused[t]--
		held.Unlock()
		return
	}
	held.Unlock()
	failIfHolding(t)
	takeFleetSlots(t, f, 1)
}

// failIfHolding fails t when it already holds a slot: taking more now would
// hold some while waiting for the rest.
func failIfHolding(t testing.TB) {
	t.Helper()
	held.Lock()
	n := held.taken[t]
	held.Unlock()
	if n > 0 {
		t.Fatal(HoldFirstMessage)
	}
}

// takeFleetSlots reserves count slots until t ends. It reports false when
// this is not a fleet run (E2E_FLEET_STATE unset: nothing to protect).
func takeFleetSlots(t testing.TB, f *fleet.Fleet, count int) bool {
	t.Helper()
	mode, err := config.FromEnv(os.LookupEnv)
	if err != nil {
		t.Fatal(err)
	}
	if mode.StatePath == "" {
		return false
	}
	capacity, err := MaxLiveFromEnv(os.LookupEnv, len(f.State.Nodes))
	if err != nil {
		t.Fatal(err)
	}
	s, err := Reserve(t.Context(), filepath.Dir(mode.StatePath), count, capacity)
	if err != nil {
		t.Fatal(err)
	}
	held.Lock()
	held.taken[t] += count
	held.Unlock()
	t.Cleanup(func() {
		held.Lock()
		if held.taken[t] -= count; held.taken[t] <= 0 {
			delete(held.taken, t)
		}
		held.Unlock()
		if err := s.Release(); err != nil {
			t.Error(err)
		}
	})
	return true
}
