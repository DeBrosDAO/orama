package namespace

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
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
// could mint TURN credentials for the namespace and read its database. It is
// now readable by its owner and the SFU's group only.
func TestWriteSFUConfig_isNotReadableByOtherLocalAccounts(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sfu-node-1.yaml")

	if err := writeSFUConfig(path, sfuTestConfig(), os.Getgid()); err != nil {
		t.Fatalf("writeSFUConfig: %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0640 {
		t.Errorf("the SFU config is mode %04o, want 0640", perm)
	}
}

// The secrets really are in there — a test asserting the mode of a file with
// nothing sensitive in it would prove nothing.
func TestWriteSFUConfig_holdsTheSecretsThatMakeTheModeMatter(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sfu-node-1.yaml")
	if err := writeSFUConfig(path, sfuTestConfig(), os.Getgid()); err != nil {
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
// write renames a fresh 0640 file over it rather than writing into it.
func TestWriteSFUConfig_replacesAFileAnOlderReleaseLeftWorldReadable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sfu-node-1.yaml")
	if err := os.WriteFile(path, []byte("listen_addr: old\n"), 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	if err := writeSFUConfig(path, sfuTestConfig(), os.Getgid()); err != nil {
		t.Fatalf("writeSFUConfig: %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm&0o007 != 0 {
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
	if err := writeSFUConfig(filepath.Join(dir, "sfu-node-1.yaml"), sfuTestConfig(), os.Getgid()); err != nil {
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

// The SFU runs as orama-sfu and reads its config through that group, so the
// file must be in the group it was written for.
func TestWriteSFUConfig_handsTheFileToTheGivenGroup(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sfu-node-1.yaml")
	gid := os.Getgid()
	if err := writeSFUConfig(path, sfuTestConfig(), gid); err != nil {
		t.Fatalf("writeSFUConfig: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		t.Fatalf("no unix stat for %s", path)
	}
	if int(st.Gid) != gid {
		t.Errorf("the SFU config is group %d, want %d", st.Gid, gid)
	}
}

// A group the writer is not in is an error, and neither the config nor a
// temp file with the secrets in it is left behind.
func TestWriteSFUConfig_refusesAGroupItCannotHandTheFileTo(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root may hand a file to any group")
	}
	gid := groupNotHeld(t)
	dir := t.TempDir()
	if err := writeSFUConfig(filepath.Join(dir, "sfu-node-1.yaml"), sfuTestConfig(), gid); err == nil {
		t.Fatalf("the config was handed to group %d, which this process is not in", gid)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("readdir: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("a failed write left %d files behind", len(entries))
	}
}

// groupNotHeld is a gid this process is not a member of.
func groupNotHeld(t *testing.T) int {
	t.Helper()
	held := map[int]bool{os.Getgid(): true}
	groups, err := os.Getgroups()
	if err != nil {
		t.Fatalf("getgroups: %v", err)
	}
	for _, g := range groups {
		held[g] = true
	}
	for gid := 1; gid < 65534; gid++ {
		if !held[gid] {
			return gid
		}
	}
	t.Fatal("this process holds every gid")
	return 0
}
