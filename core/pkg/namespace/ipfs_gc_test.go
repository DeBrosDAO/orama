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
func TestIPFSGC_GoesThroughTheDaemonAPI(t *testing.T) {
	unit := readUnit(t, "orama-namespace-ipfs-gc@.service")
	if !strings.Contains(unit, "ExecStart=/usr/local/bin/ipfs --api=${IPFS_API} --api-auth=${IPFS_API_AUTH} repo gc") {
		t.Errorf("GC does not authenticate to the daemon API:\n%s", unit)
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
