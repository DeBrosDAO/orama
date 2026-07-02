package namespace

import (
	"os"
	"path/filepath"
	"testing"

	"go.uber.org/zap"
)

// Bugboard #130 — cluster-state.json carries the namespace TURN shared secret
// (plaintext HMAC), so every writer of it must produce a 0600 file and tighten
// any pre-existing world-readable file on rewrite. SaveClusterState is the
// RECEIVER-side writer that persists state pushed from the coordinator to a
// remote namespace node; without this it landed 0644.

func TestSaveClusterState_writes0600(t *testing.T) {
	base := t.TempDir()
	s := &SystemdSpawner{namespaceBase: base, logger: zap.NewNop()}

	if err := s.SaveClusterState("ns-test", []byte(`{"turn_shared_secret":"sek-123"}`)); err != nil {
		t.Fatalf("SaveClusterState: %v", err)
	}

	path := filepath.Join(base, "ns-test", "cluster-state.json")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat cluster-state.json: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0600 {
		t.Errorf("cluster-state.json mode = %o; want 0600 (it carries the TURN secret)", perm)
	}
	// No leftover temp file from the atomic write.
	if _, err := os.Stat(path + ".tmp"); !os.IsNotExist(err) {
		t.Errorf("temp file should not survive a successful save; stat err = %v", err)
	}
}

func TestSaveClusterState_tightensExisting0644(t *testing.T) {
	base := t.TempDir()
	s := &SystemdSpawner{namespaceBase: base, logger: zap.NewNop()}

	// Simulate a file an older release wrote world-readable.
	dir := filepath.Join(base, "ns-test")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "cluster-state.json")
	if err := os.WriteFile(path, []byte(`{"old":true}`), 0644); err != nil {
		t.Fatal(err)
	}

	if err := s.SaveClusterState("ns-test", []byte(`{"turn_shared_secret":"sek-new"}`)); err != nil {
		t.Fatalf("SaveClusterState: %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat cluster-state.json: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0600 {
		t.Errorf("rewrite did not tighten perms: mode = %o; want 0600", perm)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != `{"turn_shared_secret":"sek-new"}` {
		t.Errorf("content not replaced atomically: %s", data)
	}
}
