package rqlite

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/DeBrosOfficial/network/pkg/discovery"
	"go.uber.org/zap"
)

// Raft node identity.
//
// rqlite defaults a node's raft id to its raft advertise address (confirmed in
// rqlite 8.43's flag validation: `if c.NodeID == "" { c.NodeID = c.RaftAdv }`).
// That makes identity a function of routing: give the same machine a new
// overlay address — a replacement, a WireGuard re-provision, a 10.0.0.x
// reassignment — and it mints a new raft id, joins as a second member, and the
// old entry stays in the configuration as a voter that can never be reached.
// Two such events on a five-voter cluster leave quorum at 3-of-7 with five live
// voters; one more failure freezes the registry.
//
// The fix is to key identity on the node's libp2p peer id, which is stable
// across every address change, and let the raft address be what it should have
// been all along: mutable routing data.
//
// rqlite has no way to rename a member in place, so an existing cluster cannot
// simply start passing -node-id: the node would join as a NEW member and leave
// its old id behind as an unreachable voter — causing, fleet-wide, the exact
// failure this exists to prevent. Which id a node uses is therefore recorded
// here, in a marker file beside its raft state, and changing it is a deliberate
// migration (see `orama node migrate-raft-id`) rather than a side effect of an
// upgrade.

// raftIDMarker is the file recording the raft id this node last started with.
// It sits in the rqlite data directory, beside raft.db, so the two are moved,
// wiped and backed up together.
const raftIDMarker = "raft-node-id"

// RaftIDMarkerName is the marker's filename, for tooling that has to write it
// on a node from the outside (the one-time id migration).
const RaftIDMarkerName = raftIDMarker

// RaftIdentity is the decision about which id a node starts rqlited with.
type RaftIdentity struct {
	// NodeID is the id to pass as -node-id. Empty means "pass nothing and let
	// rqlite default to the raft advertise address", which is what a node
	// predating this scheme is already registered under.
	NodeID string

	// Migrated reports whether NodeID is the stable peer-id form.
	Migrated bool
}

// ResolveRaftIdentity decides which raft id this node must start with, and
// records it.
//
// The three cases are distinguished by what is on disk, and the marker is
// written in all of them so that after one boot on this binary every node
// states its own id explicitly rather than being inferred:
//
//   - Marker present: authoritative. Whatever it says is what rqlite's
//     persisted configuration has this node under, and starting with anything
//     else would create a second member.
//   - No marker, no raft state: a fresh node. It has no configuration to
//     contradict, so it starts life on the stable id.
//   - No marker, raft state present: a node from before this scheme. Its id is
//     its raft advertise address. It keeps it until migrated.
func ResolveRaftIdentity(dataDir, peerID, raftAdvAddress string, hasState bool) (RaftIdentity, error) {
	if peerID == "" {
		// Nothing to be stable about. Rather than invent an id, fall through to
		// rqlite's default so behaviour is unchanged from before this existed.
		return RaftIdentity{}, nil
	}

	recorded, err := ReadRaftIDMarker(dataDir)
	if err != nil {
		return RaftIdentity{}, err
	}

	switch {
	case recorded != "":
		return RaftIdentity{NodeID: recorded, Migrated: recorded == peerID}, nil

	case hasState:
		// Record what rqlite is already using, so the migration has something
		// concrete to remove rather than having to re-derive it later from an
		// address that may by then have changed.
		if err := WriteRaftIDMarker(dataDir, raftAdvAddress); err != nil {
			return RaftIdentity{}, err
		}
		return RaftIdentity{}, nil

	default:
		if err := WriteRaftIDMarker(dataDir, peerID); err != nil {
			return RaftIdentity{}, err
		}
		return RaftIdentity{NodeID: peerID, Migrated: true}, nil
	}
}

// ReadRaftIDMarker returns the recorded raft id, or "" when there is none.
func ReadRaftIDMarker(dataDir string) (string, error) {
	data, err := os.ReadFile(filepath.Join(dataDir, raftIDMarker))
	if os.IsNotExist(err) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("read raft id marker: %w", err)
	}
	return strings.TrimSpace(string(data)), nil
}

// WriteRaftIDMarker records the raft id, atomically.
//
// Atomically because a torn marker is worse than no marker: a half-written id
// reads as an id nothing is registered under, and the node would start as a
// member the cluster has never heard of.
func WriteRaftIDMarker(dataDir, id string) error {
	if strings.TrimSpace(id) == "" {
		return fmt.Errorf("refusing to record an empty raft id")
	}
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return fmt.Errorf("create data dir %s: %w", dataDir, err)
	}

	path := filepath.Join(dataDir, raftIDMarker)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(id+"\n"), 0o644); err != nil {
		return fmt.Errorf("write raft id marker: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("commit raft id marker: %w", err)
	}
	return nil
}

// RaftNodeID reports the raft id this node is registered under.
//
// It reads the marker rather than assuming, because the answer differs by node
// during the migration and getting it wrong is what mints a duplicate voter:
// announce an id the cluster does not have this node under and orphan recovery
// concludes the node is missing and adds it a second time.
//
// With no marker it falls back to the raft advertise address, which is both
// rqlite's own default and what a node predating this scheme is registered
// under.
func (r *RQLiteManager) RaftNodeID() string {
	dataDir, err := r.rqliteDataDirPath()
	if err != nil {
		r.logger.Warn("Cannot locate the rqlite data directory to read this node's raft id; "+
			"falling back to the raft advertise address", zap.Error(err))
		return r.discoverConfig.RaftAdvAddress
	}

	recorded, readErr := ReadRaftIDMarker(dataDir)
	if readErr != nil {
		// Announcing an id the cluster does not have this node under is what
		// mints a duplicate voter, so a marker that exists but cannot be read
		// is worth saying out loud rather than quietly treating as absent.
		r.logger.Warn("Cannot read this node's raft id marker; falling back to the raft advertise address",
			zap.String("path", dataDir), zap.Error(readErr))
		return r.discoverConfig.RaftAdvAddress
	}
	if recorded != "" {
		return recorded
	}
	return r.discoverConfig.RaftAdvAddress
}

// isSelfPeer reports whether a discovery announcement is this node's own.
//
// Matched on the raft ADDRESS, which is unique to a machine and is what every
// caller actually means by "me". The comparisons this replaces were against the
// announced node id, which worked only while an id was an address: the moment a
// node announces a stable peer id, it stops recognising itself and starts
// counting itself as a peer — over-counting the cluster in the minimum-size
// wait, comparing its own log index against itself when picking a recovery
// source, and including itself in the active-peer list.
func isSelfPeer(meta *discovery.RQLiteNodeMetadata, selfRaftAddr string) bool {
	if meta == nil || selfRaftAddr == "" {
		return false
	}
	return meta.RaftAddress == selfRaftAddr
}
