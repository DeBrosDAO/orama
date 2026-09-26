package rqlite

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	bolt "go.etcd.io/bbolt"
)

// rqlite's on-disk layout (rqlite v8 store/store.go, rqlite/raft-boltdb).
const (
	raftDBFile        = "raft.db"
	raftSnapshotsDir  = "rsnapshots"
	raftSnapshotMeta  = "meta.json"
	raftLogsBucket    = "logs"
	raftDBLockTimeout = time.Second
)

// ErrRaftStateLocked means rqlited has raft.db open, so its state can only be
// read from the running node.
var ErrRaftStateLocked = errors.New("raft.db is held by a running rqlited")

// HasRaftState reports whether dataDir holds raft state: a snapshot, or at
// least one entry in the raft log. It is rqlite's own test (store.HasData,
// hashicorp raft HasExistingState), not whether raft.db exists — rqlited
// creates raft.db before it joins, so a join that fails leaves one behind with
// nothing in it. Reading that file as membership is what turned a node whose
// join was refused into a single-node cluster of its own.
func HasRaftState(dataDir string) (bool, error) {
	snap, err := hasSnapshot(filepath.Join(dataDir, raftSnapshotsDir))
	if err != nil || snap {
		return snap, err
	}

	path := filepath.Join(dataDir, raftDBFile)
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return false, nil
	} else if err != nil {
		return false, fmt.Errorf("stat %s: %w", path, err)
	}

	db, err := bolt.Open(path, 0o600, &bolt.Options{ReadOnly: true, Timeout: raftDBLockTimeout})
	if errors.Is(err, bolt.ErrTimeout) {
		return false, fmt.Errorf("%s: %w", path, ErrRaftStateLocked)
	}
	if err != nil {
		return false, fmt.Errorf("open %s: %w", path, err)
	}
	defer db.Close()

	var hasEntry bool
	err = db.View(func(tx *bolt.Tx) error {
		if b := tx.Bucket([]byte(raftLogsBucket)); b != nil {
			k, _ := b.Cursor().First()
			hasEntry = k != nil
		}
		return nil
	})
	if err != nil {
		return false, fmt.Errorf("read the raft log in %s: %w", path, err)
	}
	return hasEntry, nil
}

// hasSnapshot reports whether dir holds a completed snapshot: a directory
// with its metadata written. rqlite writes into "<id>.tmp" and renames.
func hasSnapshot(dir string) (bool, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("list snapshots in %s: %w", dir, err)
	}
	for _, e := range entries {
		if !e.IsDir() || strings.HasSuffix(e.Name(), ".tmp") {
			continue
		}
		if _, err := os.Stat(filepath.Join(dir, e.Name(), raftSnapshotMeta)); err == nil {
			return true, nil
		}
	}
	return false, nil
}

// NodeHasRaftState is HasRaftState for a data directory rqlited may be running
// on. While it runs it holds raft.db locked, and the node itself reports the
// same fact: a raft log that is not empty.
func NodeHasRaftState(ctx context.Context, dataDir, httpAddr, authFile string) (bool, error) {
	has, err := HasRaftState(dataDir)
	if !errors.Is(err, ErrRaftStateLocked) {
		return has, err
	}
	user, pass, err := readRQLiteAuthFile(authFile)
	if err != nil {
		return false, fmt.Errorf("rqlited is running on %s; cannot ask it for its raft state: %w", dataDir, err)
	}
	st, err := NewAdminClient("http://"+httpAddr, user, pass).Status(ctx)
	if err != nil {
		return false, fmt.Errorf("rqlited is running on %s; ask it for its raft state at %s: %w", dataDir, httpAddr, err)
	}
	return st.Store.Raft.LastLogIndex > 0 || st.Store.Raft.LastSnapshotIndex > 0, nil
}
