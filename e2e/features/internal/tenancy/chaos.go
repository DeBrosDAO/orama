//go:build e2e_fleet

package tenancy

import (
	"context"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/harness/fleet"
)

// ActiveSince is the unit's ActiveEnterTimestampMonotonic: it changes exactly
// when the unit is (re)started, so comparing two readings tells a restart from
// an in-place recovery.
func ActiveSince(t testing.TB, f *fleet.Fleet, node fleet.Node, unit string) string {
	t.Helper()
	out := f.MustExec(t, node, "systemctl show -p ActiveEnterTimestampMonotonic --value "+unit)
	return strings.TrimSpace(out.Stdout)
}

// Freeze stops every process of unit with SIGSTOP: the unit stays "active",
// so neither systemd nor the tenant reconciler restarts it, but nothing it
// serves answers — a hung process, which a stop would not simulate (the
// reconciler starts a stopped unit within a sweep). The cleanup, registered
// first, always resumes it.
func Freeze(t testing.TB, f *fleet.Fleet, node fleet.Node, unit string) {
	t.Helper()
	t.Cleanup(func() { Thaw(t, f, node, unit) })
	f.MustExec(t, node, "systemctl kill --signal=SIGSTOP "+unit)
}

// Thaw resumes a frozen unit (SIGCONT is harmless on a running one).
func Thaw(t testing.TB, f *fleet.Fleet, node fleet.Node, unit string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), fleet.CleanupBudget)
	defer cancel()
	out, err := f.SSH(ctx, node).Run(ctx, "systemctl kill --signal=SIGCONT "+unit)
	if err != nil || out.Exit != 0 {
		t.Errorf("failed to resume %s on %s (exit %d): %v %s", unit, node.Name, out.Exit, err, out.Stderr)
	}
}
