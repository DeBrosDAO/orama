//go:build e2e_fleet

package smoke

import (
	"bytes"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/e2e/harness"
)

const (
	// nodeUnit is the node's main service; every core node runs it.
	nodeUnit = "orama-node.service"
	// journalWindow is how much journal a failure attaches.
	journalWindow = 30 * time.Minute
)

func TestNodes_mainServiceActive(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	for _, n := range f.State.Nodes {
		if state := f.Unit(t, n, nodeUnit); state != "active" {
			t.Errorf("%s: %s is %q\n%s", n.Name, nodeUnit, state, f.Redact(f.Journal(t, n, nodeUnit, time.Now().Add(-journalWindow))))
		}
	}
}

// TestNodes_fileRoundTripIsCleanedUp proves the node-level plumbing every
// other feature relies on: SSH with the pinned host key, file upload and
// download, and the cleanup that removes what a test left behind.
func TestNodes_fileRoundTripIsCleanedUp(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	n := f.State.Nodes[0]
	path := "/tmp/e2e-smoke-" + f.State.RunID
	want := []byte("smoke\n")
	t.Run("write", func(t *testing.T) {
		f.WriteFile(t, n, path, want, 0o600)
		if got := f.ReadFile(t, n, path); !bytes.Equal(got, want) {
			t.Fatalf("read back %q, want %q", got, want)
		}
	})
	if out := f.Exec(t, n, "test -e "+path); out.Exit == 0 {
		t.Fatalf("%s still exists on %s after the subtest's cleanup", path, n.Name)
	}
}
