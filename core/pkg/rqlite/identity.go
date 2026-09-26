package rqlite

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
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
// reassignment, or a release that moves the index raft port — and it mints a
// new raft id, and the node is no longer a member of its own configuration:
// the persisted configuration names the old id, rqlited runs under the new
// one, and the node never rejoins.
//
// So the id a node runs under is recorded, in a marker beside its raft state,
// and passed to rqlited explicitly on every start. It never follows the
// address. A node predating stable ids keeps the address id it is registered
// under (changing it is `orama node migrate-raft-id`); a fresh node starts on
// its libp2p peer id.
//
// The address is recorded too (raftAddrMarker): the one rqlited was last
// confirmed a member at. rqlite keeps a member's address in the raft
// configuration, and a node that restarts on a new address stays unreachable
// at the old one until the leader re-registers it. rqlite does that when the
// node joins again with the same id and the new address ("Modifying a node's
// Raft network addresses": pass the new address and -join; the leader removes
// the node and re-adds it at the new address). A recorded address that differs
// from the current one is therefore what tells the index supervisor to pass
// -join on a restart (pkg/namespace indexJoinTargets).

// raftIDMarker is the file recording the raft id this node last started with.
// It sits in the rqlite data directory, beside raft.db, so the two are moved,
// wiped and backed up together.
const raftIDMarker = "raft-node-id"

// RaftIDMarkerName is the marker's filename, for tooling that has to write it
// on a node from the outside (the one-time id migration).
const RaftIDMarkerName = raftIDMarker

// raftAddrMarker is the file recording the raft address this node was last
// confirmed a member at, beside the id marker.
const raftAddrMarker = "raft-adv-addr"

// RaftAddrMarkerName is the address marker's filename, for tooling outside
// this package.
const RaftAddrMarkerName = raftAddrMarker

// raftSuffrageMarker records whether this node was last confirmed a voter or
// a non-voter, so a node that joins again after an address change keeps its
// suffrage rather than rejoining as a voter.
const raftSuffrageMarker = "raft-suffrage"

// RaftSuffrageMarkerName is the suffrage marker's filename.
const RaftSuffrageMarkerName = raftSuffrageMarker

// Suffrage values, as the marker and rqlite's /status spell them.
const (
	SuffrageVoter    = "Voter"
	SuffrageNonvoter = "Nonvoter"
)

// RaftIdentity is the decision about which id a node starts rqlited with.
type RaftIdentity struct {
	// NodeID is the id to pass as -node-id. Empty only on a node with neither
	// raft state nor a peer id, where rqlite's default (the raft advertise
	// address) is what the node will be registered under.
	NodeID string

	// Migrated reports whether NodeID is the stable peer-id form.
	Migrated bool

	// PreviousAddr is the raft address this node was last confirmed a member
	// at, "" when none is recorded. It differs from the current advertise
	// address exactly when the configuration holds this node at an address it
	// no longer listens on.
	PreviousAddr string

	// NonVoter reports whether the node was last confirmed a non-voter. A
	// rejoin passes -raft-non-voter for it: rqlite adds a joining node with
	// the suffrage the join asks for, and a non-voter rejoining as a voter
	// grows the voter set behind the voter reconciliation's back.
	NonVoter bool
}

// AddressChanged reports whether the raft configuration holds this node at
// an address other than raftAdv, so the node has to join again for the leader
// to re-register it.
func (id RaftIdentity) AddressChanged(raftAdv string) bool {
	return id.PreviousAddr != "" && id.PreviousAddr != raftAdv
}

// ResolveRaftIdentity decides which raft id this node must start with, and
// records it.
//
//   - Marker present: authoritative. Whatever it says is what rqlite's
//     persisted configuration has this node under, and starting with anything
//     else would create a second member.
//   - No marker, no raft state: a fresh node. It has no configuration to
//     contradict, so it starts life on the stable id.
//   - No marker, raft state present: refused. The node predates recorded ids,
//     so its id is the raft advertise address it last ran under, and nothing
//     on this node says what that was: node.yaml has been regenerated, and a
//     release that moves the raft port changes it. Guessing the current
//     address is exactly what leaves a node outside its own configuration.
//     The upgrade records the id from the running rqlited before it stops
//     anything (CaptureLiveIdentity), so this is reached only by a node that
//     was not upgraded that way.
func ResolveRaftIdentity(dataDir, peerID, raftAdvAddress string, hasState bool) (RaftIdentity, error) {
	recorded, err := ReadRaftIDMarker(dataDir)
	if err != nil {
		return RaftIdentity{}, err
	}
	previous, err := ReadRaftAddrMarker(dataDir)
	if err != nil {
		return RaftIdentity{}, err
	}
	suffrage, err := readMarker(dataDir, raftSuffrageMarker)
	if err != nil {
		return RaftIdentity{}, err
	}

	switch {
	case recorded != "":
		return RaftIdentity{NodeID: recorded, Migrated: recorded == peerID, PreviousAddr: previous,
			NonVoter: suffrage == SuffrageNonvoter}, nil

	case hasState:
		return RaftIdentity{}, unrecordedIdentityError(dataDir, raftAdvAddress)

	case peerID == "":
		// Nothing to be stable about and nothing to contradict: rqlite's
		// default id is the address this new node will be registered under.
		return RaftIdentity{}, nil

	default:
		if err := WriteRaftIDMarker(dataDir, peerID); err != nil {
			return RaftIdentity{}, err
		}
		return RaftIdentity{NodeID: peerID, Migrated: true}, nil
	}
}

// unrecordedIdentityError is the refusal for raft state without a recorded
// id, naming both ways forward.
func unrecordedIdentityError(dataDir, raftAdvAddress string) error {
	return fmt.Errorf("refusing to start the index rqlite: %s holds raft state but no %s, so the raft id this node "+
		"is registered under is unknown (it predates recorded ids; its id is the raft address it last ran under, "+
		"which need not be %s). Upgrade nodes with `orama node upgrade --env <env>` from this release's CLI, which "+
		"records the id from the running rqlited before stopping it; or write the id `/nodes` on a live member lists "+
		"for this node into %s",
		dataDir, raftIDMarker, raftAdvAddress, filepath.Join(dataDir, raftIDMarker))
}

// LiveIdentity is what a running rqlited reports about itself: the id it runs
// under, the address its configuration holds it at, and every member's
// address.
type LiveIdentity struct {
	NodeID  string
	Addr    string
	Members []string
	// Suffrage is SuffrageVoter or SuffrageNonvoter, from this node's entry.
	Suffrage string
}

// LiveIdentityFromStatus reads a node's identity out of its /status.
//
// The node's own address is taken from the configuration (store.nodes), not
// from its flags: the configuration is what the other members dial, and a
// node that is not in its own configuration is not a member at all.
func LiveIdentityFromStatus(st *RQLiteStatus) (LiveIdentity, error) {
	if st == nil || st.Store.NodeID == "" {
		return LiveIdentity{}, fmt.Errorf("rqlite /status carried no store.node_id")
	}
	if err := ValidateRaftID(st.Store.NodeID); err != nil {
		return LiveIdentity{}, fmt.Errorf("rqlite /status: %w", err)
	}
	live := LiveIdentity{NodeID: st.Store.NodeID}
	for _, n := range st.Store.Nodes {
		if n.Addr == "" {
			continue
		}
		if err := ValidateRaftAddress(n.Addr); err != nil {
			return LiveIdentity{}, fmt.Errorf("rqlite /status: %w", err)
		}
		live.Members = append(live.Members, n.Addr)
		if n.ID == st.Store.NodeID {
			live.Addr = n.Addr
			live.Suffrage = SuffrageVoter
			if n.Suffrage == SuffrageNonvoter {
				live.Suffrage = SuffrageNonvoter
			}
		}
	}
	if live.Addr == "" {
		return LiveIdentity{}, fmt.Errorf("rqlite runs as %q but its raft configuration has no member by that id; "+
			"this node is not a member of its own configuration — repair it (`orama node recover-raft`) before upgrading",
			st.Store.NodeID)
	}
	return live, nil
}

// peerIDPattern is a libp2p peer id: base58btc.
var peerIDPattern = regexp.MustCompile(`^[1-9A-HJ-NP-Za-km-z]{16,128}$`)

// ValidateRaftID checks that id is a raft id a node runs under: a libp2p peer
// id, or a raft address on a node that predates recorded ids. Raft ids are
// written into markers, recovery peers.json files and -node-id flags, so
// nothing else passes.
func ValidateRaftID(id string) error {
	if peerIDPattern.MatchString(id) || ValidateRaftAddress(id) == nil {
		return nil
	}
	return fmt.Errorf("raft id %q is neither a peer id nor a raft address", id)
}

// CheckRecordedID refuses a recorded id that disagrees with the one rqlited
// runs under. Both are supposed to be the same fact; if they are not, one of
// them is wrong, and recording over either would hide which.
func CheckRecordedID(recorded string, live LiveIdentity) error {
	if recorded != "" && recorded != live.NodeID {
		return fmt.Errorf("the recorded raft id %q is not the id rqlited runs under (%q); "+
			"find out which is right before restarting this node", recorded, live.NodeID)
	}
	return nil
}

// ReadRaftIDMarker returns the recorded raft id, or "" when there is none.
func ReadRaftIDMarker(dataDir string) (string, error) {
	return readMarker(dataDir, raftIDMarker)
}

// ReadRaftAddrMarker returns the raft address this node was last confirmed a
// member at, or "" when none is recorded.
func ReadRaftAddrMarker(dataDir string) (string, error) {
	return readMarker(dataDir, raftAddrMarker)
}

func readMarker(dataDir, name string) (string, error) {
	data, err := os.ReadFile(filepath.Join(dataDir, name))
	if os.IsNotExist(err) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("read %s: %w", name, err)
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
	return writeMarker(dataDir, raftIDMarker, id)
}

// WriteRaftSuffrageMarker records whether this node is a voter.
func WriteRaftSuffrageMarker(dataDir, suffrage string) error {
	if suffrage != SuffrageVoter && suffrage != SuffrageNonvoter {
		return fmt.Errorf("refusing to record suffrage %q", suffrage)
	}
	return writeMarker(dataDir, raftSuffrageMarker, suffrage)
}

// WriteRaftAddrMarker records addr as the raft address this node is a member
// at.
func WriteRaftAddrMarker(dataDir, addr string) error {
	if err := ValidateRaftAddress(addr); err != nil {
		return fmt.Errorf("refusing to record the raft address: %w", err)
	}
	return writeMarker(dataDir, raftAddrMarker, addr)
}

func writeMarker(dataDir, name, value string) error {
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return fmt.Errorf("create data dir %s: %w", dataDir, err)
	}
	return writeFileDurable(filepath.Join(dataDir, name), []byte(value+"\n"), 0o644)
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
