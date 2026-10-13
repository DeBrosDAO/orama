package rqlite

import (
	"os"
	"path/filepath"
	"testing"

	"go.uber.org/zap"
)

// What a stagenet namespace's data directory held after its first raft
// recovery (2026-10-01): rqlited removed recovery.db and left the rest.
func writeLeftovers(t *testing.T, dir string) {
	t.Helper()
	for _, name := range []string{"recovery.db-wal", "recovery.db-shm", "restore-wal-0.tmp", "restore-wal-1.tmp"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func TestRemoveRecoveryLeftovers_removesOnlyRecoveryScratch(t *testing.T) {
	dir := t.TempDir()
	writeLeftovers(t, dir)
	keep := []string{"raft.db", "db.sqlite", "db.sqlite-wal", "rqlite-auth.json"}
	for _, name := range keep {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	removed, err := RemoveRecoveryLeftovers(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(removed) != 4 {
		t.Fatalf("removed %v, want the 4 recovery files", removed)
	}
	for _, name := range []string{"recovery.db-wal", "recovery.db-shm", "restore-wal-0.tmp", "restore-wal-1.tmp"} {
		if _, err := os.Stat(filepath.Join(dir, name)); !os.IsNotExist(err) {
			t.Errorf("%s still exists", name)
		}
	}
	for _, name := range keep {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Errorf("%s was removed: %v", name, err)
		}
	}
}

func TestRemoveRecoveryLeftovers_cleanDirAndMissingDir(t *testing.T) {
	removed, err := RemoveRecoveryLeftovers(t.TempDir())
	if err != nil || len(removed) != 0 {
		t.Fatalf("clean dir: removed=%v err=%v", removed, err)
	}
	removed, err = RemoveRecoveryLeftovers(filepath.Join(t.TempDir(), "absent"))
	if err != nil || len(removed) != 0 {
		t.Fatalf("missing dir: removed=%v err=%v", removed, err)
	}
}

func TestRemoveRecoveryLeftovers_failureNamesTheFile(t *testing.T) {
	dir := t.TempDir()
	// A non-empty directory named like a leftover cannot be removed by os.Remove.
	stuck := filepath.Join(dir, "recovery.db-wal")
	if err := os.MkdirAll(filepath.Join(stuck, "child"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := RemoveRecoveryLeftovers(dir); err == nil {
		t.Fatal("want an error naming the file rqlite cannot recover past")
	}
}

func TestWritePeersJSON_clearsRecoveryLeftoversFirst(t *testing.T) {
	dir := t.TempDir()
	writeLeftovers(t, dir)
	is := &InstanceSpawner{logger: zap.NewNop()}

	if err := is.WritePeersJSON(dir, []RaftPeer{{ID: "10.0.0.1:10046", Address: "10.0.0.1:10046"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "recovery.db-wal")); !os.IsNotExist(err) {
		t.Fatal("recovery.db-wal survived a peers.json write; the recovery it triggers would fail")
	}
	if _, err := os.Stat(filepath.Join(dir, "raft", "peers.json")); err != nil {
		t.Fatalf("peers.json not written: %v", err)
	}
}
