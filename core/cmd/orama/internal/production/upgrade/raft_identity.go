package upgrade

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"time"

	oramainstall "github.com/DeBrosOfficial/network/pkg/install"
	"github.com/DeBrosOfficial/network/pkg/rootfs"
	"github.com/DeBrosOfficial/network/pkg/rqlite"
)

// statusTimeout bounds the one /status read the capture makes.
const statusTimeout = 10 * time.Second

// oramaUserName owns the rqlite data directory the markers are written into.
const oramaUserName = "orama"

// raftDBName is rqlite's raft log; its presence is raft state.
const raftDBName = "raft.db"

// markerStore is the index rqlite data directory as the capture sees it.
type markerStore interface {
	// read returns a small file's contents, "" when it does not exist.
	read(name string) (string, error)
	// write replaces a file (in the data directory, or the membership record
	// beside it) with data.
	write(name string, data []byte) error
	// hasRaftState reports whether the directory holds a raft log.
	hasRaftState() (bool, error)
}

// recordRaftIdentity records, from the running index rqlited, the raft id it
// runs under, the address its configuration holds it at and the cluster's
// member addresses (rqlite.ResolveRaftIdentity and pkg/namespace
// indexJoinTargets read them when the node starts again).
func (o *Orchestrator) recordRaftIdentity() error {
	store, err := newRootMarkerStore(o.oramaDir)
	if err != nil {
		return err
	}
	return captureRaftIdentity(store, localIndexStatus, time.Now())
}

// captureRaftIdentity is recordRaftIdentity against store, with status reading
// the local rqlited.
//
// A node without raft state has nothing to record. A node whose rqlited does
// not answer — re-running an upgrade that already stopped it — needs its id
// recorded already; without one the identity is unknown and the upgrade stops
// here, with nothing stopped yet.
func captureRaftIdentity(store markerStore, status func() (*rqlite.RQLiteStatus, error), now time.Time) error {
	hasState, err := store.hasRaftState()
	if err != nil {
		return err
	}
	recordedID, err := store.read(rqlite.RaftIDMarkerName)
	if err != nil {
		return err
	}
	st, statusErr := status()
	if statusErr != nil {
		return identityWithoutRQLite(store, hasState, recordedID, statusErr)
	}

	live, err := rqlite.LiveIdentityFromStatus(st)
	if err != nil {
		return err
	}
	if err := rqlite.CheckRecordedID(recordedID, live); err != nil {
		return err
	}
	return applyIdentityCapture(store, recordedID, live, now)
}

// identityWithoutRQLite decides a capture whose rqlited does not answer — an
// upgrade re-run after the first attempt stopped the node. Without raft state
// there is nothing to record. With it, both the id and the address must
// already be recorded: a node that has the id and not the address (a crash
// between the two writes) would restart on its new address without joining
// again, and stay outside its configuration.
func identityWithoutRQLite(store markerStore, hasState bool, recordedID string, statusErr error) error {
	if !hasState {
		fmt.Printf("  Index rqlite not answering (%v) and no raft state: nothing to record\n", statusErr)
		return nil
	}
	recordedAddr, err := store.read(rqlite.RaftAddrMarkerName)
	if err != nil {
		return err
	}
	if recordedID != "" && recordedAddr != "" {
		fmt.Printf("  Index rqlite not answering (%v); raft id %s at %s already recorded\n", statusErr, recordedID, recordedAddr)
		return nil
	}
	return fmt.Errorf("the index rqlite holds raft state but does not answer, and its raft id and address are not both "+
		"recorded, so what this node is registered under cannot be learnt; start it (`orama node start`) and re-run: %w", statusErr)
}

// applyIdentityCapture writes what the capture learnt: the id when none is
// recorded, the address, and the membership record.
func applyIdentityCapture(store markerStore, recordedID string, live rqlite.LiveIdentity, now time.Time) error {
	if recordedID == "" {
		if err := store.write(rqlite.RaftIDMarkerName, []byte(live.NodeID+"\n")); err != nil {
			return fmt.Errorf("record the raft id: %w", err)
		}
	}
	recordedAddr, err := store.read(rqlite.RaftAddrMarkerName)
	if err != nil {
		return err
	}
	if recordedAddr != live.Addr {
		if err := store.write(rqlite.RaftAddrMarkerName, []byte(live.Addr+"\n")); err != nil {
			return fmt.Errorf("record the raft address: %w", err)
		}
	}
	if err := store.write(rqlite.RaftSuffrageMarkerName, []byte(live.Suffrage+"\n")); err != nil {
		return fmt.Errorf("record the suffrage: %w", err)
	}
	if err := recordMembers(store, live.Members, now); err != nil {
		return err
	}
	fmt.Printf("  ✓ raft id %s at %s, %d member(s)\n", live.NodeID, live.Addr, len(live.Members))
	return nil
}

// recordMembers merges members into the membership record.
func recordMembers(store markerStore, members []string, now time.Time) error {
	raw, err := store.read(rqlite.ClusterMembershipFileName)
	if err != nil {
		return err
	}
	var existing *rqlite.ClusterMembership
	if raw != "" {
		var m rqlite.ClusterMembership
		// A torn record is replaced: this node's running rqlited is the
		// evidence of membership now.
		if json.Unmarshal([]byte(raw), &m) == nil {
			existing = &m
		}
	}
	record, changed, err := rqlite.MergeMembership(existing, members, now)
	if err != nil || !changed {
		return err
	}
	data, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return fmt.Errorf("encode the membership record: %w", err)
	}
	if err := store.write(rqlite.ClusterMembershipFileName, append(data, '\n')); err != nil {
		return fmt.Errorf("write the membership record: %w", err)
	}
	return nil
}

// localIndexStatus reads the local index rqlited's /status, where node.yaml
// says it binds (a 0.122.x node.yaml included; see rqlite.preAuthConfig).
func localIndexStatus() (*rqlite.RQLiteStatus, error) {
	ep, err := rqlite.LocalNodeEndpoint()
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), statusTimeout)
	defer cancel()
	return ep.Admin().Status(ctx)
}

// rootMarkerStore is the index rqlite data directory, written as root through
// rootfs (no symlink is followed in a directory the orama user owns) and
// handed to the orama user, whose rqlited and orama-node rewrite these files.
type rootMarkerStore struct {
	root     rootfs.Root
	dir      string
	uid, gid int
}

func newRootMarkerStore(oramaDir string) (*rootMarkerStore, error) {
	u, err := user.Lookup(oramaUserName)
	if err != nil {
		return nil, fmt.Errorf("look up the %s user: %w", oramaUserName, err)
	}
	uid, err := strconv.Atoi(u.Uid)
	if err != nil {
		return nil, fmt.Errorf("the %s user's uid %q: %w", oramaUserName, u.Uid, err)
	}
	gid, err := strconv.Atoi(u.Gid)
	if err != nil {
		return nil, fmt.Errorf("the %s user's gid %q: %w", oramaUserName, u.Gid, err)
	}
	return &rootMarkerStore{
		root: oramainstall.OramaRoot(oramaDir),
		dir:  filepath.Join(oramaDir, "data", "rqlite"),
		uid:  uid, gid: gid,
	}, nil
}

// path places the membership record beside the data directory, as
// rqlite.ClusterMembershipPath does, and everything else inside it.
func (s *rootMarkerStore) path(name string) string {
	if name == rqlite.ClusterMembershipFileName {
		return rqlite.ClusterMembershipPath(s.dir)
	}
	return filepath.Join(s.dir, name)
}

func (s *rootMarkerStore) read(name string) (string, error) {
	data, err := s.root.ReadFile(s.path(name), rootfs.SmallFileLimit)
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("read %s: %w", s.path(name), err)
	}
	return string(trimNewline(data)), nil
}

func (s *rootMarkerStore) write(name string, data []byte) error {
	p := s.path(name)
	if err := s.root.WriteFile(p, data, 0o644); err != nil {
		return err
	}
	return s.root.Chown(p, s.uid, s.gid)
}

func (s *rootMarkerStore) hasRaftState() (bool, error) {
	info, err := os.Lstat(filepath.Join(s.dir, raftDBName))
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return false, nil
	case err != nil:
		return false, fmt.Errorf("inspect the raft log: %w", err)
	default:
		return info.Mode().IsRegular(), nil
	}
}

func trimNewline(b []byte) []byte {
	for len(b) > 0 && (b[len(b)-1] == '\n' || b[len(b)-1] == '\r' || b[len(b)-1] == ' ') {
		b = b[:len(b)-1]
	}
	return b
}
