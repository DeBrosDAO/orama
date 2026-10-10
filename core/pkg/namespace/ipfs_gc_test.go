package namespace

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func readUnit(t *testing.T, name string) string {
	t.Helper()
	_, file, _, _ := runtime.Caller(0)
	data, err := os.ReadFile(filepath.Join(filepath.Dir(file), "..", "..", "systemd", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// GC must go through the running daemon's API; a bare `ipfs repo gc` fell
// back to an offline GC during daemon start-up and failed on the repo lock.
// The unit runs `orama node ipfs-gc`, which takes the daemon's address and
// bearer from the environment file written here, and not Kubo's own CLI: that
// answered the SIGTERM of a planned stop by waiting for the collection, so the
// stop timed out and the unit stayed failed.
func TestIPFSGC_GoesThroughTheDaemonAPI(t *testing.T) {
	unit := readUnit(t, "orama-namespace-ipfs-gc@.service")
	if !strings.Contains(unit, "\nExecStart=/opt/orama/bin/orama node ipfs-gc\n") {
		t.Errorf("GC is not run by orama's own command:\n%s", unit)
	}
	if strings.Contains(unit, "\nExecStart=/usr/local/bin/ipfs") {
		t.Errorf("GC runs Kubo's CLI, which does not stop on SIGTERM:\n%s", unit)
	}
	if !strings.Contains(unit, "\nEnvironmentFile=/var/lib/orama-unit-env/%i/ipfs-gc.env\n") {
		t.Errorf("GC does not read the daemon address and bearer from its environment file:\n%s", unit)
	}
	env := ipfsGCEnv("/repo", "n1", "bearer:abc")
	if env["IPFS_API"] == "" || !strings.HasPrefix(env["IPFS_API"], "/ip4/127.0.0.1/tcp/") {
		t.Errorf("IPFS_API = %q", env["IPFS_API"])
	}
	if env["IPFS_API_AUTH"] != "bearer:abc" {
		t.Errorf("IPFS_API_AUTH = %q", env["IPFS_API_AUTH"])
	}
}

// An OnBootSec deadline is long past on a running host, so every restart of
// the timer fired GC immediately, while the daemon was still starting.
func TestIPFSGCTimer_CountsFromActivationNotBoot(t *testing.T) {
	timer := readUnit(t, "orama-namespace-ipfs-gc@.timer")
	if strings.Contains(timer, "\nOnBootSec=") || !strings.Contains(timer, "\nOnActiveSec=") {
		t.Errorf("timer must use OnActiveSec, not OnBootSec:\n%s", timer)
	}
}
