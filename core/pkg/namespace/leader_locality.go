package namespace

import (
	"context"
	"net"
	"path/filepath"
	"time"

	"github.com/DeBrosOfficial/network/pkg/rqlite"
	"go.uber.org/zap"
)

// Bugboard #708 — namespace raft leadership is geography-blind: the initial
// leader is sortedNodeIDs[0] over random libp2p peer IDs, and raft re-elects
// freely on every restart. When a geographically-distant node (high WireGuard
// RTT to its peers) becomes the leader, EVERY namespace write funnels through
// the distant node and waits on its cross-region replication for quorum — each
// rqlite hop jumps from ~20ms (co-located) to ~256ms, stacking into 5-10s RPCs
// that break calling.
//
// This reconciler keeps namespace leadership on a co-located voter. It NEVER
// removes a node or changes voter membership — all nodes stay voters (quorum
// and fault tolerance unchanged). It only hands leadership OFF a node that is
// isolated from the rest of the cluster, using rqlite's own
// transfer-leadership API.
const (
	// leaderLocalityInterval is how often each node checks whether the
	// namespace clusters it leads are well-placed.
	leaderLocalityInterval = 90 * time.Second
	// leaderLocalityRTTThreshold: if the leader's CLOSEST voter peer is farther
	// than this, the leader is treated as geographically isolated and hands off
	// leadership. Co-located nodes are ~20ms apart; a distant node is ~256ms —
	// 100ms cleanly separates the two without false positives.
	leaderLocalityRTTThreshold = 100 * time.Millisecond
	// leaderLocalityCooldown bounds how often a single namespace's leadership
	// is moved. In the common topology (a lone distant node among co-located
	// peers) ONE transfer settles leadership on a co-located voter, which then
	// stays (it has a nearby peer, so it never re-triggers). In a pathological
	// all-mutually-distant topology there is no good leader to move to and the
	// nearest-peer transfer would rotate; the cooldown caps that to roughly one
	// transfer per node per window (bounded, non-destructive — membership and
	// quorum are never touched), and node selection clustering most nodes
	// ~20ms apart makes that case rare.
	leaderLocalityCooldown = 10 * time.Minute
	// leaderLocalityDialTimeout bounds each per-peer RTT probe.
	leaderLocalityDialTimeout = 3 * time.Second
)

// decideLeadershipTransfer is the pure decision: should the local leader hand
// off leadership, and to which voter? peerRTTs maps each OTHER reachable voter's
// raft address → measured RTT. Returns a target and true ONLY when this node is
// the leader, every voter is reachable (don't destabilize an already-degraded
// cluster), the cooldown has elapsed, and even the CLOSEST peer is farther than
// `threshold` — i.e. the leader is isolated. If the leader has at least one
// nearby voter it is central enough; leave it. The chosen target is the nearest
// reachable peer (which, in a 1-distant/N-close topology, is a co-located node
// that will then have a nearby peer of its own → stable).
func decideLeadershipTransfer(isLeader, allVotersReachable, cooldownElapsed bool, peerRTTs map[string]time.Duration, threshold time.Duration) (string, bool) {
	if !isLeader || !allVotersReachable || !cooldownElapsed || len(peerRTTs) == 0 {
		return "", false
	}
	var bestAddr string
	var bestRTT time.Duration
	for addr, rtt := range peerRTTs {
		if bestAddr == "" || rtt < bestRTT {
			bestAddr, bestRTT = addr, rtt
		}
	}
	if bestRTT > threshold {
		return bestAddr, true
	}
	return "", false
}

// measurePeerRTTs probes every OTHER voter's raft address and returns their
// RTTs plus whether ALL voters were reachable+measurable (so the caller can
// refuse to act on a degraded cluster). Non-voters and self are skipped.
func measurePeerRTTs(nodes rqlite.RQLiteNodes, selfID string) (map[string]time.Duration, bool) {
	peerRTTs := make(map[string]time.Duration)
	allReachable := true
	for _, n := range nodes {
		if !n.Voter || n.ID == selfID {
			continue
		}
		if !n.Reachable {
			allReachable = false
			continue
		}
		dialAddr := n.Addr
		if dialAddr == "" {
			dialAddr = n.ID
		}
		rtt, derr := measureRaftRTT(dialAddr, leaderLocalityDialTimeout)
		if derr != nil {
			allReachable = false
			continue
		}
		peerRTTs[n.ID] = rtt
	}
	return peerRTTs, allReachable
}

// measureRaftRTT returns the TCP-connect time to a peer's raft address — a
// privilege-free proxy for WireGuard round-trip latency.
func measureRaftRTT(raftAddr string, timeout time.Duration) (time.Duration, error) {
	start := time.Now()
	conn, err := net.DialTimeout("tcp", raftAddr, timeout)
	if err != nil {
		return 0, err
	}
	_ = conn.Close()
	return time.Since(start), nil
}

func (cm *ClusterManager) leaderTransferCooldownElapsed(namespace string) bool {
	cm.leaderLocalityMu.Lock()
	defer cm.leaderLocalityMu.Unlock()
	last, ok := cm.leaderLocalityCooldown[namespace]
	return !ok || time.Since(last) >= leaderLocalityCooldown
}

func (cm *ClusterManager) recordLeaderTransfer(namespace string) {
	cm.leaderLocalityMu.Lock()
	defer cm.leaderLocalityMu.Unlock()
	if cm.leaderLocalityCooldown == nil {
		cm.leaderLocalityCooldown = make(map[string]time.Time)
	}
	cm.leaderLocalityCooldown[namespace] = time.Now()
}

// StartLeaderLocalityReconciler runs the periodic leadership-locality check
// until ctx is cancelled. Safe to call once at node boot.
func (cm *ClusterManager) StartLeaderLocalityReconciler(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(leaderLocalityInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				cm.reconcileLeaderLocality(ctx)
			}
		}
	}()
}

// reconcileLeaderLocality checks every namespace cluster this node hosts and,
// for any it currently leads from an isolated position, transfers leadership to
// the nearest co-located voter.
func (cm *ClusterManager) reconcileLeaderLocality(ctx context.Context) {
	pattern := filepath.Join(cm.baseDataDir, "*", "cluster-state.json")
	matches, err := filepath.Glob(pattern)
	if err != nil {
		cm.logger.Debug("leader-locality: glob failed", zap.Error(err))
		return
	}
	for _, path := range matches {
		if ctx.Err() != nil {
			return
		}
		state, err := loadLocalState(path)
		if err != nil {
			continue
		}
		cm.reconcileNamespaceLeader(state.NamespaceName, state.LocalPorts.RQLiteHTTPPort)
	}
}

// reconcileNamespaceLeader handles a single namespace's leadership locality.
func (cm *ClusterManager) reconcileNamespaceLeader(namespace string, rqliteHTTPPort int) {
	if rqliteHTTPPort == 0 {
		return
	}
	status, err := rqlite.GetRaftStatus(rqliteHTTPPort)
	if err != nil {
		// rqlite not up / not reachable on this node — nothing to do.
		return
	}
	if status.Store.Raft.State != "Leader" {
		return // only the leader can transfer leadership away
	}
	selfID := status.Store.Raft.LeaderID

	nodes, err := rqlite.GetRaftNodes(rqliteHTTPPort)
	if err != nil {
		return
	}

	peerRTTs, allVotersReachable := measurePeerRTTs(nodes, selfID)

	target, transfer := decideLeadershipTransfer(
		true, allVotersReachable, cm.leaderTransferCooldownElapsed(namespace),
		peerRTTs, leaderLocalityRTTThreshold,
	)
	if !transfer {
		return
	}

	cm.logger.Info("leader-locality: this node is an isolated namespace raft leader — transferring leadership to a co-located voter (bugboard #708)",
		zap.String("namespace", namespace),
		zap.String("from", selfID),
		zap.String("to", target),
		zap.Duration("target_rtt", peerRTTs[target]),
	)
	// Record the cooldown BEFORE the transfer so a slow/looping transfer can't
	// re-fire on the next tick regardless of outcome.
	cm.recordLeaderTransfer(namespace)
	if err := rqlite.TransferLeadershipTo(rqliteHTTPPort, target, cm.logger); err != nil {
		cm.logger.Warn("leader-locality: leadership transfer failed",
			zap.String("namespace", namespace), zap.Error(err))
	}
}
