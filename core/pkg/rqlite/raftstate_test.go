package rqlite

import (
	"context"
	"encoding/binary"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	bolt "go.etcd.io/bbolt"
)

// writeRaftDB creates raft.db the way rqlited does: the logs and conf buckets,
// with n entries in the log.
func writeRaftDB(t *testing.T, dir string, n int) string {
	t.Helper()
	path := filepath.Join(dir, raftDBFile)
	db, err := bolt.Open(path, 0o600, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	err = db.Update(func(tx *bolt.Tx) error {
		logs, err := tx.CreateBucketIfNotExists([]byte(raftLogsBucket))
		if err != nil {
			return err
		}
		if _, err := tx.CreateBucketIfNotExists([]byte("conf")); err != nil {
			return err
		}
		for i := 1; i <= n; i++ {
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
	return path
}

func TestHasRaftState_emptyDirectory(t *testing.T) {
	has, err := HasRaftState(t.TempDir())
	if err != nil || has {
		t.Fatalf("HasRaftState(empty) = %v, %v; want false, nil", has, err)
	}
}

// The superman join: rqlited created raft.db, the join was refused, and the
// next start read the file's existence as membership, dropped -join, and
// bootstrapped a second cluster. A raft.db with an empty log is not a member.
func TestHasRaftState_raftDBLeftByAFailedJoin(t *testing.T) {
	dir := t.TempDir()
	path := writeRaftDB(t, dir, 0)
	if info, err := os.Stat(path); err != nil || info.Size() <= 1024 {
		t.Fatalf("the fixture must look like the file the old size check accepted: %v, %v", info, err)
	}

	has, err := HasRaftState(dir)
	if err != nil {
		t.Fatalf("HasRaftState: %v", err)
	}
	if has {
		t.Fatal("an empty raft log was read as raft state; the node would bootstrap instead of joining")
	}
}

func TestHasRaftState_raftLogWithEntries(t *testing.T) {
	dir := t.TempDir()
	writeRaftDB(t, dir, 3)
	has, err := HasRaftState(dir)
	if err != nil || !has {
		t.Fatalf("HasRaftState = %v, %v; want true, nil", has, err)
	}
}

// A log truncated after a snapshot is still a member.
func TestHasRaftState_snapshotWithoutLog(t *testing.T) {
	dir := t.TempDir()
	snap := filepath.Join(dir, raftSnapshotsDir, "2-519-1790390231116")
	if err := os.MkdirAll(snap, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(snap, raftSnapshotMeta), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	has, err := HasRaftState(dir)
	if err != nil || !has {
		t.Fatalf("HasRaftState = %v, %v; want true, nil", has, err)
	}
}

func TestHasRaftState_ignoresUnfinishedSnapshots(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"2-519-1790390231116.tmp", "3-600-1790390231117"} {
		if err := os.MkdirAll(filepath.Join(dir, raftSnapshotsDir, name), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	tmpMeta := filepath.Join(dir, raftSnapshotsDir, "2-519-1790390231116.tmp", raftSnapshotMeta)
	if err := os.WriteFile(tmpMeta, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	has, err := HasRaftState(dir)
	if err != nil || has {
		t.Fatalf("HasRaftState = %v, %v; want false: a .tmp snapshot and one with no meta.json are not snapshots", has, err)
	}
}

func TestHasRaftState_unreadableRaftDB(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, raftDBFile), []byte("not a bolt file"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := HasRaftState(dir); err == nil {
		t.Fatal("a raft.db that is not a bolt file was read as a verdict")
	}
}

// holdRaftDB opens raft.db read-write, as a running rqlited does.
func holdRaftDB(t *testing.T, dir string) {
	t.Helper()
	db, err := bolt.Open(filepath.Join(dir, raftDBFile), 0o600, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
}

func TestHasRaftState_lockedByARunningNode(t *testing.T) {
	dir := t.TempDir()
	writeRaftDB(t, dir, 0)
	holdRaftDB(t, dir)

	_, err := HasRaftState(dir)
	if !errors.Is(err, ErrRaftStateLocked) {
		t.Fatalf("err = %v, want ErrRaftStateLocked", err)
	}
}

func statusAt(t *testing.T, lastLogIndex int) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if user, pass, ok := r.BasicAuth(); !ok || user != "orama" || pass != "secret" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"store":{"raft":{"last_log_index":` + strconv.Itoa(lastLogIndex) + `}}}`))
	}))
	t.Cleanup(srv.Close)
	return strings.TrimPrefix(srv.URL, "http://")
}

func authFileIn(t *testing.T, dir string) string {
	t.Helper()
	path := filepath.Join(dir, AuthFileName)
	if err := os.WriteFile(path, []byte(`[{"username":"orama","password":"secret","perms":["all"]}]`), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestNodeHasRaftState_asksTheRunningNode(t *testing.T) {
	for _, tc := range []struct {
		lastLogIndex int
		want         bool
	}{{0, false}, {5, true}} {
		dir := t.TempDir()
		writeRaftDB(t, dir, 0)
		holdRaftDB(t, dir)

		has, err := NodeHasRaftState(context.Background(), dir, statusAt(t, tc.lastLogIndex), authFileIn(t, dir))
		if err != nil {
			t.Fatalf("NodeHasRaftState: %v", err)
		}
		if has != tc.want {
			t.Errorf("last_log_index %d: has = %v, want %v", tc.lastLogIndex, has, tc.want)
		}
	}
}

func TestNodeHasRaftState_readsTheDiskWhenNothingRuns(t *testing.T) {
	dir := t.TempDir()
	writeRaftDB(t, dir, 2)
	has, err := NodeHasRaftState(context.Background(), dir, "127.0.0.1:1", filepath.Join(dir, "absent.json"))
	if err != nil || !has {
		t.Fatalf("NodeHasRaftState = %v, %v; want true from raft.db without asking anyone", has, err)
	}
}

func TestNodeHasRaftState_runningNodeThatDoesNotAnswer(t *testing.T) {
	dir := t.TempDir()
	writeRaftDB(t, dir, 0)
	holdRaftDB(t, dir)

	srv := httptest.NewServer(http.NotFoundHandler())
	addr := strings.TrimPrefix(srv.URL, "http://")
	srv.Close()

	if _, err := NodeHasRaftState(context.Background(), dir, addr, authFileIn(t, dir)); err == nil {
		t.Fatal("a running node that could not be asked produced a verdict")
	}
}
