package lifecycle

import (
	"reflect"
	"testing"
)

// TestSelectFrontendToRestore pins the restore policy behind the bare-restart
// caddy fix: a restart must bring back exactly the frontend units (caddy,
// coredns) that were running before orama-node's stop cascade-killed them —
// no more, no less.
func TestSelectFrontendToRestore(t *testing.T) {
	// Both active → both restored, order preserved.
	got := selectFrontendToRestore([]string{"coredns", "caddy"}, func(string) bool { return true })
	if !reflect.DeepEqual(got, []string{"coredns", "caddy"}) {
		t.Errorf("both active: expected [coredns caddy], got %v", got)
	}

	// Only caddy active (e.g. a non-nameserver node where coredns isn't running)
	// → only caddy is restored.
	got = selectFrontendToRestore([]string{"coredns", "caddy"}, func(svc string) bool {
		return svc == "caddy"
	})
	if !reflect.DeepEqual(got, []string{"caddy"}) {
		t.Errorf("only caddy: expected [caddy], got %v", got)
	}

	// Nothing active (units absent or intentionally stopped) → restore nothing,
	// so a restart never starts a frontend that was deliberately down.
	got = selectFrontendToRestore([]string{"coredns", "caddy"}, func(string) bool { return false })
	if len(got) != 0 {
		t.Errorf("none active: expected empty, got %v", got)
	}

	// Empty candidate list is safe.
	if got := selectFrontendToRestore(nil, func(string) bool { return true }); len(got) != 0 {
		t.Errorf("nil candidates: expected empty, got %v", got)
	}
}
