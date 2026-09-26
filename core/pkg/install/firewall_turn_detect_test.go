package install

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func turnSetup(t *testing.T) (*ProductionSetup, string) {
	t.Helper()
	oramaDir := filepath.Join(t.TempDir(), ".orama")
	return &ProductionSetup{oramaDir: oramaDir}, oramaDir
}

func writeTURNFile(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func assertRunsTURN(t *testing.T, ps *ProductionSetup, want bool, why string) {
	t.Helper()
	got, err := ps.hostRunsTURN()
	if err != nil {
		t.Fatalf("%s: %v", why, err)
	}
	if got != want {
		t.Fatalf("%s: hostRunsTURN = %v, want %v", why, got, want)
	}
}

// Bugboard #846: Phase 6b must find a TURN node from files, since the upgrade
// has stopped the TURN units by then. It reads the layout the node is on: the
// current shared config, or — until orama-node has moved it — the old one.
func TestHostRunsTURN_eachLayoutLocationCounts(t *testing.T) {
	for _, tc := range []struct{ name, rel string }{
		{"current shared config", "data/turn/turn.yaml"},
		{"old shared config", "configs/turn.yaml"},
		{"old per-namespace env", "data/namespaces/anchat/turn.env"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ps, oramaDir := turnSetup(t)
			writeTURNFile(t, filepath.Join(oramaDir, tc.rel))
			assertRunsTURN(t, ps, true, tc.name)
		})
	}
}

func TestHostRunsTURN_noTURNAnywhere(t *testing.T) {
	ps, oramaDir := turnSetup(t)
	assertRunsTURN(t, ps, false, "an empty node")

	writeTURNFile(t, filepath.Join(oramaDir, "data", "namespaces", "gw-only", "gateway.env"))
	assertRunsTURN(t, ps, false, "a gateway-only namespace")
}

// A location that cannot be read is not evidence of no TURN: guessing "no"
// closes the relay range on a TURN node.
func TestHostRunsTURN_unreadableNamespacesDirIsAnError(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads any directory")
	}
	ps, oramaDir := turnSetup(t)
	nsDir := filepath.Join(oramaDir, "data", "namespaces")
	if err := os.MkdirAll(nsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(nsDir, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(nsDir, 0o700) })
	if _, err := ps.hostRunsTURN(); err == nil {
		t.Fatal("an unreadable namespaces directory must be an error")
	}
}

// Root reads a directory the orama user owns here. A FIFO or symlink planted
// as data/namespaces must fail the phase at once, not block it forever with
// the node's services stopped.
func TestHostRunsTURN_plantedFIFOOrSymlinkFailsFast(t *testing.T) {
	for _, kind := range []string{"fifo", "symlink"} {
		t.Run(kind, func(t *testing.T) {
			ps, oramaDir := turnSetup(t)
			nsDir := filepath.Join(oramaDir, "data", "namespaces")
			if err := os.MkdirAll(filepath.Dir(nsDir), 0o755); err != nil {
				t.Fatal(err)
			}
			var err error
			if kind == "fifo" {
				err = syscall.Mkfifo(nsDir, 0o600)
			} else {
				err = os.Symlink(t.TempDir(), nsDir)
			}
			if err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() { _, err := ps.hostRunsTURN(); done <- err }()
			select {
			case err := <-done:
				if err == nil {
					t.Fatalf("a %s in place of data/namespaces must be an error", kind)
				}
			case <-time.After(5 * time.Second):
				t.Fatalf("hostRunsTURN blocked on a %s", kind)
			}
		})
	}
}
