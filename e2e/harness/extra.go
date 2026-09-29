package harness

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/e2e/harness/fleet"
	"github.com/DeBrosOfficial/network/e2e/harness/provision"
	"github.com/DeBrosOfficial/network/e2e/harness/runctx"
)

// extraBudget bounds creating or removing one extra server.
const extraBudget = 15 * time.Minute

// Extra is a server ExtraNode created: the node, and the SHA256:...
// fingerprint of the host key the provisioner pinned for it, the value
// `orama node setup --host-key` takes.
type Extra struct {
	fleet.Node
	HostKey string
}

// ExtraNode creates an extra server for this test (nothing installed on it)
// and deletes it when the test ends. name must be unique in the run. The
// shared state file is not modified; the extra is recorded in the package's
// fleet (under its lock), so Lookup, Node and AllNodes find it while it lives.
func ExtraNode(t testing.TB, name, location string) Extra {
	t.Helper()
	f := Fleet(t)
	own := *f.State
	own.Extras = append([]fleet.Node{}, f.State.Extras...)
	run, release := runctx.With(t.Context())
	defer release()
	ctx, cancel := context.WithTimeout(run, extraBudget)
	defer cancel()
	n, err := provision.AddExtra(ctx, &own, name, location)
	if err != nil {
		t.Fatalf("failed to create extra server %s: %v", name, err)
	}
	t.Cleanup(func() {
		f.RemoveExtra(name)
		cctx, ccancel := context.WithTimeout(context.Background(), extraBudget)
		defer ccancel()
		if err := provision.RemoveExtra(cctx, &own, name); err != nil {
			t.Errorf("cleanup: failed to delete extra server %s (the run's label sweep will): %v", name, err)
		}
	})
	if err := f.AddExtra(n); err != nil {
		t.Fatal(err)
	}
	fp, err := fleet.HostKeyFingerprint(&own, n)
	if err != nil {
		t.Fatal(errors.Join(errors.New("the extra server's host key is not pinned"), err))
	}
	return Extra{Node: n, HostKey: fp}
}
