package rqlite

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/config"
	"go.uber.org/zap"
)

// managerWithDataDir builds a manager rooted at a temp directory, and returns
// the rqlite data dir the readers below look in.
func managerWithDataDir(t *testing.T) (*RQLiteManager, string) {
	t.Helper()
	root := t.TempDir()
	r := &RQLiteManager{
		dataDir:        root,
		logger:         zap.NewNop(),
		config:         &config.DatabaseConfig{},
		discoverConfig: &config.DiscoveryConfig{},
	}
	dir := filepath.Join(root, "rqlite")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	return r, dir
}

func writeSnapshot(t *testing.T, dir, name string, meta any) {
	t.Helper()
	snapDir := filepath.Join(dir, "rsnapshots", name)
	if err := os.MkdirAll(snapDir, 0o755); err != nil {
		t.Fatalf("mkdir snapshot: %v", err)
	}
	var body []byte
	switch v := meta.(type) {
	case string:
		body = []byte(v)
	default:
		var err error
		body, err = json.Marshal(v)
		if err != nil {
			t.Fatalf("marshal meta: %v", err)
		}
	}
	if err := os.WriteFile(filepath.Join(snapDir, "meta.json"), body, 0o644); err != nil {
		t.Fatalf("write meta: %v", err)
	}
}

func TestGetPersistedRaftLogIndex_absentDirectoryIsATrustworthyZero(t *testing.T) {
	// A node that has taken no snapshots genuinely has no snapshot index. That
	// is the ONE zero a caller may act on.
	r, _ := managerWithDataDir(t)

	index, known := r.getPersistedRaftLogIndex()
	if index != 0 || !known {
		t.Fatalf("got (%d, %v), want (0, true)", index, known)
	}
}

func TestGetPersistedRaftLogIndex_readsTheHighestIndex(t *testing.T) {
	r, dir := managerWithDataDir(t)
	writeSnapshot(t, dir, "snap-1", struct {
		Index uint64 `json:"Index"`
	}{Index: 42})
	writeSnapshot(t, dir, "snap-2", struct {
		Index uint64 `json:"Index"`
	}{Index: 99})

	index, known := r.getPersistedRaftLogIndex()
	if index != 99 || !known {
		t.Fatalf("got (%d, %v), want (99, true)", index, known)
	}
}

func TestGetPersistedRaftLogIndex_unparseableMetaIsUnknownNotZero(t *testing.T) {
	// This is the bug. An unreadable snapshot used to produce the same zero as
	// an empty node, and the caller deletes the raft log on a zero.
	r, dir := managerWithDataDir(t)
	writeSnapshot(t, dir, "snap-1", struct {
		Index uint64 `json:"Index"`
	}{Index: 500})
	writeSnapshot(t, dir, "snap-2", "{ this is not json")

	index, known := r.getPersistedRaftLogIndex()
	if known {
		t.Fatalf("an unparseable snapshot reported a KNOWN index of %d; "+
			"the caller destroys the raft log on a known zero", index)
	}
}

func TestGetPersistedRaftLogIndex_unreadableMetaIsUnknown(t *testing.T) {
	r, dir := managerWithDataDir(t)
	writeSnapshot(t, dir, "snap-1", struct {
		Index uint64 `json:"Index"`
	}{Index: 7})

	metaPath := filepath.Join(dir, "rsnapshots", "snap-1", "meta.json")
	if err := os.Chmod(metaPath, 0o000); err != nil {
		t.Skipf("cannot make the file unreadable here: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(metaPath, 0o644) })

	if _, known := r.getPersistedRaftLogIndex(); known {
		t.Fatal("an unreadable snapshot reported a known index")
	}
}

func TestClearRaftState_movesEverythingAsideRatherThanDeleting(t *testing.T) {
	r, dir := managerWithDataDir(t)

	// The full layout: the log, the snapshots it was compacted into, and the
	// recorded membership.
	if err := os.WriteFile(filepath.Join(dir, "raft.db"), []byte("log"), 0o644); err != nil {
		t.Fatalf("write raft.db: %v", err)
	}
	writeSnapshot(t, dir, "snap-1", struct {
		Index uint64 `json:"Index"`
	}{Index: 12})
	if err := os.MkdirAll(filepath.Join(dir, "raft"), 0o755); err != nil {
		t.Fatalf("mkdir raft: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "raft", "peers.json"), []byte("[]"), 0o644); err != nil {
		t.Fatalf("write peers.json: %v", err)
	}

	if err := r.clearRaftState(dir); err != nil {
		t.Fatalf("clearRaftState: %v", err)
	}

	// Nothing is left in place.
	for _, name := range []string{"raft.db", "rsnapshots", "raft"} {
		if _, err := os.Stat(filepath.Join(dir, name)); !os.IsNotExist(err) {
			t.Errorf("%s was left in place", name)
		}
	}

	// Everything is recoverable. rsnapshots was previously left behind, which
	// produced a node with snapshots and no log to apply them against.
	discarded := findDiscardedDir(t, dir)
	for _, name := range []string{"raft.db", "rsnapshots", "raft"} {
		if _, err := os.Stat(filepath.Join(discarded, name)); err != nil {
			t.Errorf("%s was destroyed rather than set aside: %v", name, err)
		}
	}
}

func TestClearRaftState_withNothingToMoveLeavesNoDirectory(t *testing.T) {
	r, dir := managerWithDataDir(t)

	if err := r.clearRaftState(dir); err != nil {
		t.Fatalf("clearRaftState: %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("an empty discard directory was left behind: %v", entries)
	}
}

func findDiscardedDir(t *testing.T, dir string) string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	for _, e := range entries {
		if e.IsDir() && len(e.Name()) > len("raft.discarded-") && e.Name()[:len("raft.discarded-")] == "raft.discarded-" {
			return filepath.Join(dir, e.Name())
		}
	}
	t.Fatal("no raft.discarded-* directory was created")
	return ""
}

func TestCheckNeedsClusterRecovery_normalPostSnapshotStateIsNotRecovery(t *testing.T) {
	// The reproduction. It used to return true whenever snapshots existed and
	// raft.db was 8 MB or smaller — which is the steady state of a healthy node
	// just after its log compacts.
	r, dir := managerWithDataDir(t)
	writeSnapshot(t, dir, "snap-1", struct {
		Index uint64 `json:"Index"`
	}{Index: 900})
	if err := os.WriteFile(filepath.Join(dir, "raft.db"), []byte("small log"), 0o644); err != nil {
		t.Fatalf("write raft.db: %v", err)
	}

	needs, err := r.checkNeedsClusterRecovery(dir)
	if err != nil {
		t.Fatalf("checkNeedsClusterRecovery: %v", err)
	}
	if needs {
		t.Fatal("a healthy node just after a snapshot was treated as needing cluster recovery")
	}
}

func TestCheckNeedsClusterRecovery_noRecordedPeersIsNotRecovery(t *testing.T) {
	r, dir := managerWithDataDir(t)

	needs, err := r.checkNeedsClusterRecovery(dir)
	if err != nil {
		t.Fatalf("checkNeedsClusterRecovery: %v", err)
	}
	if needs {
		t.Fatal("a node with no recorded membership has nothing to be stale about")
	}
}

func TestRecordedPeerAddresses(t *testing.T) {
	r, dir := managerWithDataDir(t)

	if got, err := r.recordedPeerAddresses(dir); err != nil || got != nil {
		t.Fatalf("absent peers.json: got (%v, %v), want (nil, nil)", got, err)
	}

	if err := os.MkdirAll(filepath.Join(dir, "raft"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "raft", "peers.json"),
		[]byte(`[{"id":"a","address":"10.0.0.2:10101"},{"id":"b","address":"10.0.0.3:10101"}]`), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	got, err := r.recordedPeerAddresses(dir)
	if err != nil {
		t.Fatalf("recordedPeerAddresses: %v", err)
	}
	if len(got) != 2 || got[0] != "10.0.0.2:10101" || got[1] != "10.0.0.3:10101" {
		t.Fatalf("got %v", got)
	}

	// A malformed file must be an error, not an empty membership — an empty
	// one reads as "nothing recorded" and suppresses recovery silently.
	if err := os.WriteFile(filepath.Join(dir, "raft", "peers.json"), []byte("{not json"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := r.recordedPeerAddresses(dir); err == nil {
		t.Fatal("a malformed peers.json was read as an empty membership")
	}
}
