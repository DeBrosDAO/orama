package namespace

import (
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/rqlite"
	bolt "go.etcd.io/bbolt"
)

// writeNamespaceRaftDB lays raft.db down where rqlite v8 keeps it — the data
// directory's root — with entries log entries.
func writeNamespaceRaftDB(t *testing.T, dataDir string, entries int) *bolt.DB {
	t.Helper()
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		t.Fatal(err)
	}
	db, err := bolt.Open(filepath.Join(dataDir, "raft.db"), 0o600, nil)
	if err != nil {
		t.Fatal(err)
	}
	err = db.Update(func(tx *bolt.Tx) error {
		logs, err := tx.CreateBucketIfNotExists([]byte("logs"))
		if err != nil {
			return err
		}
		for i := 1; i <= entries; i++ {
			key := make([]byte, 8)
			binary.BigEndian.PutUint64(key, uint64(i))
			if err := logs.Put(key, []byte("entry")); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return db
}

// The bug: the check looked for <dataDir>/raft, which rqlite v8 never
// creates, so a member with a full raft log counted as new and peers.json
// recovery never ran.
func TestNamespaceHasRaftState_raftLogAtTheDataDirRoot(t *testing.T) {
	dataDir := filepath.Join(t.TempDir(), "rqlite", "node-1")
	writeNamespaceRaftDB(t, dataDir, 3).Close()

	has, err := namespaceHasRaftState(dataDir)
	if err != nil || !has {
		t.Fatalf("a member with a raft log at the data dir root: %v, %v", has, err)
	}
}

// The raft/ subdirectory holds only the peers.json this manager writes; it is
// not membership.
func TestNamespaceHasRaftState_peersJSONAloneIsNotState(t *testing.T) {
	dataDir := filepath.Join(t.TempDir(), "rqlite", "node-1")
	if err := os.MkdirAll(filepath.Join(dataDir, "raft"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dataDir, "raft", "peers.json"), []byte("[]"), 0o644); err != nil {
		t.Fatal(err)
	}
	has, err := namespaceHasRaftState(dataDir)
	if err != nil || has {
		t.Fatalf("raft/peers.json alone: %v, %v", has, err)
	}
}

func TestNamespaceHasRaftState_newMemberHasNone(t *testing.T) {
	for name, dataDir := range map[string]string{
		"missing dir": filepath.Join(t.TempDir(), "absent"),
		"empty raft.db (failed join)": func() string {
			d := filepath.Join(t.TempDir(), "n")
			writeNamespaceRaftDB(t, d, 0).Close()
			return d
		}(),
	} {
		has, err := namespaceHasRaftState(dataDir)
		if err != nil || has {
			t.Errorf("%s: %v, %v", name, has, err)
		}
	}
}

// A process holding raft.db while its unit is down leaves membership unknown:
// the restore stops rather than guessing.
func TestNamespaceHasRaftState_heldRaftDBIsAnError(t *testing.T) {
	dataDir := filepath.Join(t.TempDir(), "rqlite", "node-1")
	db := writeNamespaceRaftDB(t, dataDir, 1)
	defer db.Close()

	_, err := namespaceHasRaftState(dataDir)
	if !errors.Is(err, rqlite.ErrRaftStateLocked) {
		t.Fatalf("expected ErrRaftStateLocked, got %v", err)
	}
}
