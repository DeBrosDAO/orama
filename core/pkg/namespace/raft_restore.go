package namespace

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"time"

	"github.com/DeBrosOfficial/network/pkg/rqlite"
	"go.uber.org/zap"
)

// namespaceHasRaftState reports whether a namespace rqlite's data directory
// holds raft state, deciding whether a restore restarts the member it was
// (peers.json recovery) or joins as a new one.
//
// rqlite v8 keeps raft.db and rsnapshots/ at the data directory's root. This
// used to look for a raft/ subdirectory, which only ever holds the peers.json
// this manager writes: false on every real member, so peers.json recovery never
// ran and a restarted member was handed -join instead.
//
// It is called only while the instance's unit is not running. rqlited holding
// raft.db anyway (rqlite.ErrRaftStateLocked) means a process outlived its unit;
// whether this node is a member cannot then be read off the disk, and guessing
// either way spawns a second rqlited or a duplicate voter, so it is an error.
func namespaceHasRaftState(dataDir string) (bool, error) {
	has, err := rqlite.HasRaftState(dataDir)
	if errors.Is(err, rqlite.ErrRaftStateLocked) {
		return false, fmt.Errorf("an rqlited still holds the raft log in %s although its unit is not running; "+
			"stop that process before this namespace is restored: %w", dataDir, err)
	}
	if err != nil {
		return false, fmt.Errorf("read the raft state in %s: %w", dataDir, err)
	}
	return has, nil
}

// Namespace raft membership, for restores.
//
// A namespace rqlited that is not running is restored by the node on its own,
// and the two decisions it makes can split the namespace: forcing a raft
// configuration with a recovery peers.json, and bootstrapping a new cluster
// when it holds no raft state. Both are now taken only on evidence of what
// this node's configuration was: the namespace's membership record, the same
// record the index keeps (rqlite.ClusterMembership), written beside the node's
// rqlite directory while its rqlited runs (recordNamespaceMembership), so the
// loss of the raft state does not take it with it.

// restoreMember is one member of a namespace cluster as the restore sees it.
type restoreMember struct {
	nodeID   string
	raftAddr string
}

// needsPeersRecovery reports whether a node holding raft state must restart
// from a recovery peers.json: only when its recorded membership differs from
// the authoritative one. peers.json is rqlite's force-recovery — it rewrites
// the raft configuration — and writing it on every restart of a namespace
// that is live elsewhere forced this node's view onto a configuration it was
// already in, or onto one the live leader had since changed. With no record
// the node's configuration is unknown, and a node that keeps its configuration
// waits for its peers, which is what raft is for.
func needsPeersRecovery(record *rqlite.ClusterMembership, authoritative []rqlite.RaftPeer) bool {
	if record == nil || len(record.Members) == 0 || len(authoritative) == 0 {
		return false
	}
	want := make([]string, 0, len(authoritative))
	for _, p := range authoritative {
		want = append(want, p.Address)
	}
	slices.Sort(want)
	want = slices.Compact(want)
	have := slices.Clone(record.Members)
	slices.Sort(have)
	return !slices.Equal(have, want)
}

// restoreJoinPlan decides how a node with no raft state restarts a namespace
// rqlite: which members it joins, or whether it bootstraps.
//
// A node that has been a member (it has a record) lost its data: it joins the
// other members and never bootstraps — electing itself leader of an empty
// cluster while the others hold the data is a split brain. With no other
// member there is nobody to recover from, and it refuses. A node that has
// never been a member takes part in the deterministic election: the lowest
// node id bootstraps and the rest join it.
func restoreJoinPlan(namespace, localNodeID string, members []restoreMember, record *rqlite.ClusterMembership, recordPath string) (joinAddrs []string, isLeader bool, err error) {
	if record != nil {
		for _, m := range members {
			if m.nodeID != localNodeID && !slices.Contains(joinAddrs, m.raftAddr) {
				joinAddrs = append(joinAddrs, m.raftAddr)
			}
		}
		if len(joinAddrs) == 0 {
			return nil, false, fmt.Errorf("refusing to bootstrap namespace %s: this node has been a member (recorded in %s) but holds "+
				"no raft state and has no other member to join; starting would create an empty cluster. Restore its data, or "+
				"delete %s to bootstrap an empty one deliberately", namespace, recordPath, recordPath)
		}
		return joinAddrs, false, nil
	}

	ids := make([]string, 0, len(members))
	for _, m := range members {
		ids = append(ids, m.nodeID)
	}
	if len(ids) == 0 {
		return nil, false, fmt.Errorf("namespace %s has no members to restore", namespace)
	}
	slices.Sort(ids)
	if ids[0] == localNodeID {
		return nil, true, nil
	}
	for _, m := range members {
		if m.nodeID == ids[0] {
			return []string{m.raftAddr}, false, nil
		}
	}
	return nil, false, nil
}

// readNamespaceMembership reads the membership record for the namespace rqlite
// data directory dataDir. A torn record on a node that still holds raft state
// is dropped (its state proves membership, and the next pass records it
// again); without state it is evidence the node refuses to start on.
func readNamespaceMembership(dataDir string, hasState bool) (*rqlite.ClusterMembership, error) {
	record, err := rqlite.ReadClusterMembership(rqlite.ClusterMembershipPath(dataDir))
	if hasState && errors.Is(err, rqlite.ErrCorruptMembershipRecord) {
		return nil, nil
	}
	return record, err
}

// recordNamespaceMembership records the running namespace rqlite's raft
// configuration beside its data directory.
func (cm *ClusterManager) recordNamespaceMembership(ctx context.Context, dataDir, host string, port int) error {
	ep, err := cm.tenantRQLiteEndpoint(host, port)
	if err != nil {
		return err
	}
	status, err := ep.Admin().Status(ctx)
	if err != nil {
		return fmt.Errorf("read the namespace rqlite's raft configuration: %w", err)
	}
	addrs := make([]string, 0, len(status.Store.Nodes))
	for _, n := range status.Store.Nodes {
		addrs = append(addrs, n.Addr)
	}
	_, err = rqlite.RecordMembership(rqlite.ClusterMembershipPath(dataDir), addrs, time.Now())
	return err
}

// planNamespaceRQLiteStart decides how a stopped namespace rqlite restarts
// from the authoritative membership (members, and the same as raft peers): a
// node holding raft state writes a recovery peers.json only when its recorded
// membership differs (needsPeersRecovery) and otherwise restarts on its own
// configuration; a node without state joins or bootstraps by restoreJoinPlan.
func (cm *ClusterManager) planNamespaceRQLiteStart(namespace, dataDir string, hasState bool, members []restoreMember, peers []rqlite.RaftPeer) ([]string, bool, error) {
	record, err := readNamespaceMembership(dataDir, hasState)
	if err != nil {
		return nil, false, err
	}
	if !hasState {
		return restoreJoinPlan(namespace, cm.localNodeID, members, record, rqlite.ClusterMembershipPath(dataDir))
	}
	if !needsPeersRecovery(record, peers) {
		cm.logger.Info("Namespace rqlite restarts on its own raft configuration",
			zap.String("namespace", namespace), zap.Bool("membership_recorded", record != nil))
		return nil, false, nil
	}
	if err := cm.writePeersJSON(dataDir, peers); err != nil {
		return nil, false, fmt.Errorf("write peers.json in %s: %w", dataDir, err)
	}
	cm.logger.Warn("Recorded raft membership differs from the live one; restarting from a recovery peers.json",
		zap.String("namespace", namespace), zap.Strings("recorded", record.Members), zap.Int("peers", len(peers)))
	return nil, false, nil
}

// recordRunningNamespace records the membership of a namespace rqlite that is
// running on this node. A failure is logged and the next restore pass tries
// again: the record is evidence for a later restart, and this pass has
// nothing to restart.
func (cm *ClusterManager) recordRunningNamespace(ctx context.Context, namespace, localIP string, httpPort int) {
	dataDir := filepath.Join(cm.baseDataDir, namespace, "rqlite", cm.localNodeID)
	if err := cm.recordNamespaceMembership(ctx, dataDir, localIP, httpPort); err != nil {
		cm.logger.Warn("Could not record the namespace rqlite's membership; the next pass retries",
			zap.String("namespace", namespace), zap.Error(err))
	}
}
