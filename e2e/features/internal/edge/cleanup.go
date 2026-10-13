//go:build e2e_fleet

package edge

import (
	"context"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/harness/fleet"
)

// RunInCleanup runs cmd on n from a t.Cleanup (whose t.Context is already
// cancelled) and reports, without stopping the other cleanups, a command that
// failed or exited non-zero: the node may be left disturbed.
func RunInCleanup(t testing.TB, f *fleet.Fleet, n fleet.Node, cmd string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), fleet.CleanupBudget)
	defer cancel()
	out, err := f.SSH(ctx, n).Run(ctx, cmd)
	if err != nil || out.Exit != 0 {
		t.Errorf("cleanup on %s failed (exit %d): %v %s — the node may be left disturbed", n.Name, out.Exit, err, f.Redact(out.Stderr))
	}
}
