package namespace

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.uber.org/zap"

	"github.com/DeBrosOfficial/network/pkg/rqlite"
)

// Bugboard #281. `orama namespace delete` left the per-node data directory on disk
// (the failures were logged and swallowed, so the delete still reported success).
// Re-creating a namespace of the same name then booted the new cluster on top of the
// old raft state: on devnet the three nodes disagreed on membership — one had
// inherited a peer set containing a long-removed node and members of a DIFFERENT
// namespace it had erroneously joined — and no leader was ever elected.
//
// A brand-new cluster must therefore never adopt raft state it finds on disk. A
// restart must still reuse it, since that is what makes a restart a restart.

func raftDirFor(base, namespace, nodeID string) string {
	return filepath.Join(base, namespace, "rqlite", nodeID)
}

// seedStaleRaftState writes a marker into the raft directory a previous incarnation
// of this namespace would have left behind.
func seedStaleRaftState(t *testing.T, base, namespace, nodeID string) string {
	t.Helper()
	dir := raftDirFor(base, namespace, nodeID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("seed raft dir: %v", err)
	}
	marker := filepath.Join(dir, "raft.db")
	if err := os.WriteFile(marker, []byte("stale raft state"), 0o644); err != nil {
		t.Fatalf("seed raft.db: %v", err)
	}
	return marker
}

// TestSpawnRQLite_freshStartClearsLeftoverRaftState is the reproduction: leftover
// state present, FreshStart set, so it must be gone before rqlited is launched.
func TestSpawnRQLite_freshStartClearsLeftoverRaftState(t *testing.T) {
	base := t.TempDir()
	marker := seedStaleRaftState(t, base, "anchat-v2", "node-1")

	s := NewSystemdSpawner(base, "", zap.NewNop())
	cfg := rqlite.InstanceConfig{
		Namespace:  "anchat-v2",
		NodeID:     "node-1",
		HTTPPort:   10005,
		RaftPort:   10006,
		FreshStart: true,
	}

	// SpawnRQLite will fail later (no systemd in a unit test); we only care that
	// the stale state is cleared before it gets that far.
	_ = s.SpawnRQLite(context.Background(), "anchat-v2", "node-1", cfg)

	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Errorf("stale raft state still present at %s (err=%v) — a fresh cluster would inherit the previous namespace's membership", marker, err)
	}
}

// TestSpawnRQLite_restartPreservesRaftState is the guard on the other side: a normal
// restart must NOT wipe the node's raft directory, or every restart would rebuild
// the cluster from nothing.
func TestSpawnRQLite_restartPreservesRaftState(t *testing.T) {
	base := t.TempDir()
	marker := seedStaleRaftState(t, base, "anchat-test", "node-1")

	s := NewSystemdSpawner(base, "", zap.NewNop())
	cfg := rqlite.InstanceConfig{
		Namespace:  "anchat-test",
		NodeID:     "node-1",
		HTTPPort:   10000,
		RaftPort:   10001,
		FreshStart: false,
	}

	_ = s.SpawnRQLite(context.Background(), "anchat-test", "node-1", cfg)

	if _, err := os.Stat(marker); err != nil {
		t.Errorf("raft state was removed on a restart (err=%v) — restarts must reuse it", err)
	}
}

// TestSpawnRQLite_freshStartWithNoExistingStateIsFine: the common case, nothing on
// disk, must not error out.
func TestSpawnRQLite_freshStartWithNoExistingStateIsFine(t *testing.T) {
	base := t.TempDir()
	s := NewSystemdSpawner(base, "", zap.NewNop())
	cfg := rqlite.InstanceConfig{
		Namespace:  "brand-new",
		NodeID:     "node-1",
		HTTPPort:   10010,
		RaftPort:   10011,
		FreshStart: true,
	}

	err := s.SpawnRQLite(context.Background(), "brand-new", "node-1", cfg)
	// Any error must come from systemd being unavailable, not from the fresh-start
	// bookkeeping.
	if err != nil && containsAny(err.Error(), "clear stale RQLite state", "inspect RQLite state dir") {
		t.Errorf("fresh start failed on a clean node: %v", err)
	}
}

func containsAny(s string, subs ...string) bool {
	for _, sub := range subs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}
