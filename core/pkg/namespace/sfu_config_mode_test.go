package namespace

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func sfuTestConfig() SFUInstanceConfig {
	return SFUInstanceConfig{
		Namespace:      "anchat-test",
		NodeID:         "node-1",
		ListenAddr:     "10.0.0.5:30000",
		MediaPortStart: 40000,
		MediaPortEnd:   40100,
		TURNSecret:     "the-namespaces-hmac-secret",
		TURNCredTTL:    86400,
		RQLiteDSN:      "http://orama:the-database-password@10.0.0.5:15000",
	}
}

// The file holds the namespace's TURN shared secret and a DSN with the database
// password in it, and it was written 0644 — so any local account on the node
// could mint TURN credentials for the namespace and read its database.
func TestWriteSFUConfig_isNotReadableByOtherLocalAccounts(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sfu-node-1.yaml")

	if err := writeSFUConfig(path, sfuTestConfig()); err != nil {
		t.Fatalf("writeSFUConfig: %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0600 {
		t.Errorf("the SFU config is mode %04o, want 0600", perm)
	}
}

// The secrets really are in there — a test asserting the mode of a file with
// nothing sensitive in it would prove nothing.
func TestWriteSFUConfig_holdsTheSecretsThatMakeTheModeMatter(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sfu-node-1.yaml")
	if err := writeSFUConfig(path, sfuTestConfig()); err != nil {
		t.Fatalf("writeSFUConfig: %v", err)
	}

	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	for _, secret := range []string{"the-namespaces-hmac-secret", "the-database-password"} {
		if !strings.Contains(string(body), secret) {
			t.Errorf("the rendered config no longer contains %q; if the secret moved, this test and "+
				"the mode it justifies should move with it", secret)
		}
	}
}

// A node upgraded from a release that wrote 0644 has such a file already. The
// write renames a fresh 0600 file over it rather than writing into it.
func TestWriteSFUConfig_replacesAFileAnOlderReleaseLeftWorldReadable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sfu-node-1.yaml")
	if err := os.WriteFile(path, []byte("listen_addr: old\n"), 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	if err := writeSFUConfig(path, sfuTestConfig()); err != nil {
		t.Fatalf("writeSFUConfig: %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0600 {
		t.Errorf("a world-readable config from an older release was left at %04o", perm)
	}

	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if strings.Contains(string(body), "old") {
		t.Error("the old contents survived the rewrite")
	}
}

// Nothing is left behind on the way: a temp file with the secret in it would be
// as readable as the thing this fixes.
func TestWriteSFUConfig_leavesNoTemporaryFileBehind(t *testing.T) {
	dir := t.TempDir()
	if err := writeSFUConfig(filepath.Join(dir, "sfu-node-1.yaml"), sfuTestConfig()); err != nil {
		t.Fatalf("writeSFUConfig: %v", err)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("readdir: %v", err)
	}
	for _, entry := range entries {
		if strings.Contains(entry.Name(), ".tmp-") {
			t.Errorf("a temp file was left behind: %s", entry.Name())
		}
	}
	if len(entries) != 1 {
		t.Errorf("expected one file, found %d", len(entries))
	}
}
