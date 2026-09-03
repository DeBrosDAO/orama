package rqlite

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"go.uber.org/zap"
)

const (
	// voterChangeCooldown is how long to wait after a failed voter change
	// before retrying the same node.
	voterChangeCooldown = 10 * time.Minute

	// voterReconcileSettleDelay and orphanRecoverySettleDelay are how long each
	// reconciler waits after start-up before its first pass. Both used to be a
	// bare time.Sleep, which meant a node that was asked to shut down inside
	// the window kept a goroutine alive for minutes past cancellation.
	voterReconcileSettleDelay = 3 * time.Minute
	orphanRecoverySettleDelay = 5 * time.Minute
)

// voterReconciler holds the state the reconciler needs across ticks: raft
// itself remembers none of it.
type voterReconciler struct {
	mu        sync.Mutex
	cooldowns map[string]time.Time // nodeID → earliest next attempt

	// unreachable counts consecutive ticks each member has failed to answer.
	unreachable unreachableStreaks

	// lastTerm and stableTerms track how long the raft term has been steady.
	// A membership change issued during an election can be lost or applied
	// against a configuration that is already gone.
	lastTerm    uint64
	stableTerms int

	// evictionCooldowns holds off a candidate whose eviction could not be
	// completed, so a failure does not produce a fresh attempt — and a fresh
	// tombstone write — every two minutes.
	evictionCooldowns map[string]time.Time
}

// evictionCooldown is how long a failed eviction attempt is left alone. Long
// enough that a transient cause has passed, short enough that a genuinely dead
// voter is still evicted within the hour.
const evictionCooldown = 10 * time.Minute

// evictionOnCooldown reports whether this candidate was recently held off.
func (v *voterReconciler) evictionOnCooldown(nodeID string) bool {
	v.mu.Lock()
	defer v.mu.Unlock()
	until, held := v.evictionCooldowns[nodeID]
	return held && time.Now().Before(until)
}

// holdOffEviction defers further attempts on this candidate.
func (v *voterReconciler) holdOffEviction(nodeID string) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.evictionCooldowns[nodeID] = time.Now().Add(evictionCooldown)
}

// termsBeforeMembershipChange is how many consecutive ticks the raft term must
// be unchanged before this node will alter the configuration.
//
// The check it replaces read /status twice microseconds apart and compared the
// term to itself, which proves only that two HTTP calls returned the same
// number. Across ticks the term is a real stability signal: at 2 minutes a
// cluster that is re-electing will change term inside the window.
const termsBeforeMembershipChange = 3

// noteTerm records this tick's raft term and reports whether the term has been
// stable long enough to change membership.
func (v *voterReconciler) noteTerm(term uint64) bool {
	v.mu.Lock()
	defer v.mu.Unlock()

	if term != v.lastTerm {
		v.lastTerm = term
		v.stableTerms = 1
		return false
	}
	v.stableTerms++
	return v.stableTerms >= termsBeforeMembershipChange
}

// startVoterReconciliation periodically checks and corrects voter/non-voter
// assignments. Only takes effect on the leader node. Corrects at most one
// node per cycle to minimize disruption.
func (r *RQLiteManager) startVoterReconciliation(ctx context.Context) {
	reconciler := &voterReconciler{
		cooldowns:         make(map[string]time.Time),
		unreachable:       make(unreachableStreaks),
		evictionCooldowns: make(map[string]time.Time),
	}

	// Wait for the cluster to stabilize after startup before correcting
	// anything: mid-boot, the raft configuration is still moving.
	select {
	case <-ctx.Done():
		return
	case <-time.After(voterReconcileSettleDelay):
	}

	ticker := time.NewTicker(2 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			r.runMembershipTick(ctx, reconciler)
		}
	}
}

// startOrphanedNodeRecovery runs every 5 minutes on the leader. It scans for
// nodes that appear in the discovery peer list but NOT in the Raft cluster
// (orphaned by a failed remove+rejoin during voter reconciliation). For each
// orphaned node, it re-adds them via POST /join. (C1 fix)
func (r *RQLiteManager) startOrphanedNodeRecovery(ctx context.Context) {
	// Wait for the cluster to stabilize before treating any node as orphaned.
	select {
	case <-ctx.Done():
		return
	case <-time.After(orphanRecoverySettleDelay):
	}

	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			r.recoverOrphanedNodes(ctx)
		}
	}
}

// recoverOrphanedNodes finds nodes known to discovery but missing from the
// Raft cluster and re-adds them.
func (r *RQLiteManager) recoverOrphanedNodes(ctx context.Context) {
	if r.discoveryService == nil {
		return
	}

	// Only the leader runs orphan recovery. Checked first so followers do no
	// work at all — the tombstone read below is a database query every node
	// would otherwise issue every five minutes for nothing.
	status, err := r.getRQLiteStatus()
	if err != nil || status.Store.Raft.State != "Leader" {
		return
	}

	// A node that was removed on purpose must not be re-added by this loop.
	// Without the check, an operator's removal and an eviction were both undone
	// within five minutes: the loop re-adds every discovery peer absent from
	// the raft configuration, and discovery keeps a peer for hours.
	db, err := r.localSQLHandle()
	if err != nil {
		r.logger.Warn("No database handle for eviction tombstones; not re-adding orphans this cycle",
			zap.Error(err))
		return
	}
	tombstoned, err := tombstonedNodes(ctx, db)
	if err != nil {
		// Fail closed: without the tombstone list this loop cannot tell an
		// orphan from a node someone deliberately removed, and re-adding the
		// latter is the more expensive mistake.
		r.logger.Warn("Cannot read raft eviction tombstones; not re-adding orphans this cycle",
			zap.Error(err))
		return
	}

	// Get all Raft cluster members
	raftNodes, err := r.getAllClusterNodes()
	if err != nil {
		return
	}
	// Keyed by raft id AND by raft address. The id is the correct key —
	// rqlite's /nodes is keyed by it, and it is what a join adds or replaces —
	// but during the migration to stable ids a peer may still announce itself
	// under its address while the cluster already holds it under its peer id,
	// or the reverse. Matching either means a node is never mistaken for
	// missing and re-added a second time under its other name, which is exactly
	// the duplicate-voter failure stable identity exists to prevent.
	raftNodeSet := make(map[string]bool, len(raftNodes)*2)
	for _, n := range raftNodes {
		raftNodeSet[n.ID] = true
		if n.Addr != "" {
			raftNodeSet[n.Addr] = true
		}
	}

	// Get all discovery peers
	discoveryPeers := r.discoveryService.GetAllPeers()

	selfID := r.RaftNodeID()

	for _, peer := range discoveryPeers {
		// A peer's raft id is its identity; its address is where to reach it.
		// Fall back to the address only for a peer whose announcement carries
		// no id, which is what a node on an older binary sends.
		peerNodeID := peer.NodeID
		if peerNodeID == "" {
			peerNodeID = peer.RaftAddress
		}

		if peerNodeID == selfID || peer.RaftAddress == r.discoverConfig.RaftAdvAddress {
			continue // skip self
		}
		if raftNodeSet[peerNodeID] || raftNodeSet[peer.RaftAddress] {
			continue // already in cluster, under either name
		}
		if _, evicted := tombstoned[peerNodeID]; evicted {
			r.logger.Debug("Node is absent from raft because it was evicted on purpose; leaving it out",
				zap.String("node_id", peerNodeID))
			continue
		}
		if _, evicted := tombstoned[peer.RaftAddress]; evicted {
			r.logger.Debug("Node is absent from raft because it was evicted on purpose; leaving it out",
				zap.String("node_raft_addr", peer.RaftAddress))
			continue
		}

		// This peer is in discovery but not in Raft — it's orphaned
		r.logger.Warn("Found orphaned node (in discovery but not in Raft cluster), re-adding",
			zap.String("node_raft_addr", peer.RaftAddress),
			zap.String("node_id", peer.NodeID))

		// Determine voter status
		raftAddrs := make([]string, 0, len(discoveryPeers))
		for _, p := range discoveryPeers {
			raftAddrs = append(raftAddrs, p.RaftAddress)
		}
		voters := computeVoterSet(raftAddrs, MaxDefaultVoters)
		_, shouldBeVoter := voters[peer.RaftAddress]

		// id and address are passed separately. They used to be the same value,
		// so re-adding a node whose address had changed created a SECOND member
		// rather than moving the existing one.
		if err := r.joinClusterNode(peerNodeID, peer.RaftAddress, shouldBeVoter); err != nil {
			r.logger.Error("Failed to re-add orphaned node",
				zap.String("node_id", peerNodeID),
				zap.String("raft_addr", peer.RaftAddress),
				zap.Bool("voter", shouldBeVoter),
				zap.Error(err))
		} else {
			r.logger.Info("Successfully re-added orphaned node to Raft cluster",
				zap.String("node_id", peerNodeID),
				zap.String("raft_addr", peer.RaftAddress),
				zap.Bool("voter", shouldBeVoter))
		}
	}
}

// runMembershipTick is one pass over raft membership: evict a member that is
// demonstrably gone, or correct the voter set, but never both in the same tick.
//
// It reads /status and /nodes once and hands the same snapshot to both passes.
// That is not only cheaper — each used to fetch its own — it is what makes
// "one membership change per tick" and "the term has been stable for N ticks"
// mean what they say. With each pass observing the term separately, a tick
// where both ran counted twice toward the same stability budget.
func (r *RQLiteManager) runMembershipTick(ctx context.Context, reconciler *voterReconciler) {
	status, err := r.getRQLiteStatus()
	if err != nil {
		r.logger.Debug("Membership tick skipped: cannot read status", zap.Error(err))
		return
	}
	if status.Store.Raft.State != "Leader" {
		return
	}

	nodes, err := r.getAllClusterNodes()
	if err != nil {
		r.logger.Debug("Membership tick skipped: cannot read nodes", zap.Error(err))
		return
	}

	termStable := reconciler.noteTerm(status.Store.Raft.Term)

	changed, err := r.evictDeadVoters(ctx, reconciler, nodes, termStable)
	if err != nil {
		// Logged, not returned: the voter reconcile below is independent work,
		// and coupling them meant a database hiccup in the eviction evidence
		// silently stopped voter correction too.
		r.logger.Warn("Dead-voter eviction failed", zap.Error(err))
	}
	if changed {
		return // one membership change per tick
	}

	if err := r.reconcileVoters(reconciler, status, nodes, termStable); err != nil {
		r.logger.Debug("Voter reconciliation skipped", zap.Error(err))
	}
}

// reconcileVoters compares the cluster's actual voter assignments against the
// deterministic desired set (computeVoterSet) and corrects one mismatch.
//
// Promotion and demotion are both a POST /join carrying the voter flag, so
// neither takes the node out of the configuration. It corrects at most one node
// per tick, skips nodes in cooldown after a failed change, and requires a live
// quorum and a term the caller has watched stay stable — see runMembershipTick.
func (r *RQLiteManager) reconcileVoters(reconciler *voterReconciler, status *RQLiteStatus, nodes RQLiteNodes, termStable bool) error {
	if len(nodes) <= MaxDefaultVoters {
		return nil // Small cluster — all nodes should be voters
	}

	// 3. Require a quorum of reachable voters, not a fully reachable cluster.
	//
	// "Every member must answer" meant one permanently dead node froze the
	// voter set for ever — which is precisely the state that most needs
	// correcting, and it is why nothing ever demoted an excess voter on a
	// cluster carrying a corpse. What actually matters is that the change can
	// be committed, so the condition is a live quorum.
	reachableVoters, totalVoters := 0, 0
	for _, n := range nodes {
		if !n.Voter {
			continue
		}
		totalVoters++
		if n.Reachable {
			reachableVoters++
		}
	}
	if reachableVoters < totalVoters/2+1 {
		return fmt.Errorf("only %d of %d voters reachable; not changing membership without a quorum",
			reachableVoters, totalVoters)
	}

	// 4. Leader stability, measured across ticks by the caller.
	//
	// This used to read /status twice, microseconds apart, and compare the term
	// to itself — which proves only that two HTTP calls returned the same
	// number. The term is only a stability signal when observed over time.
	if !termStable {
		return fmt.Errorf("raft term has not been stable for %d ticks; deferring membership change",
			termsBeforeMembershipChange)
	}

	// 5. Compute desired voter set from raft addresses
	// Addresses, not ids. computeVoterSet orders candidates by overlay IP, and
	// the other two call sites feed it addresses; feeding it ids here made the
	// three disagree the moment ids stopped being addresses, so orphan recovery
	// would promote a node this pass then demoted, for ever.
	raftAddrs := make([]string, 0, len(nodes))
	addrByID := make(map[string]string, len(nodes))
	for _, n := range nodes {
		addr := n.Addr
		if addr == "" {
			addr = n.ID
		}
		raftAddrs = append(raftAddrs, addr)
		addrByID[n.ID] = addr
	}
	desiredVoters := computeVoterSet(raftAddrs, MaxDefaultVoters)

	// 6. Safety: never demote ourselves (the current leader)
	myRaftAddr := status.Store.Raft.LeaderID
	if _, shouldBeVoter := desiredVoters[myRaftAddr]; !shouldBeVoter {
		r.logger.Warn("Leader is not in computed voter set — skipping reconciliation",
			zap.String("leader_id", myRaftAddr))
		return nil
	}

	// 7. Find one mismatch to fix (one change per cycle)
	for _, n := range nodes {
		// The voter set is keyed by address; the action is taken on the id.
		_, shouldBeVoter := desiredVoters[addrByID[n.ID]]

		// Check cooldown
		reconciler.mu.Lock()
		cooldownUntil, hasCooldown := reconciler.cooldowns[n.ID]
		if hasCooldown && time.Now().Before(cooldownUntil) {
			reconciler.mu.Unlock()
			continue
		}
		reconciler.mu.Unlock()

		if n.Voter && !shouldBeVoter {
			// Skip if this is the leader
			if n.ID == myRaftAddr {
				continue
			}

			r.logger.Info("Demoting excess voter to non-voter",
				zap.String("node_id", n.ID))

			if err := r.setVoterInPlace(n.ID, false); err != nil {
				r.logger.Warn("Failed to demote voter",
					zap.String("node_id", n.ID),
					zap.Error(err))
				reconciler.mu.Lock()
				reconciler.cooldowns[n.ID] = time.Now().Add(voterChangeCooldown)
				reconciler.mu.Unlock()
				return err
			}

			r.logger.Info("Successfully demoted voter to non-voter",
				zap.String("node_id", n.ID))
			return nil // One change per cycle
		}

		if !n.Voter && shouldBeVoter {
			r.logger.Info("Promoting non-voter to voter",
				zap.String("node_id", n.ID))

			// Try direct promotion first (POST /join with voter=true)
			if err := r.joinClusterNode(n.ID, n.ID, true); err == nil {
				r.logger.Info("Successfully promoted non-voter to voter (direct join)",
					zap.String("node_id", n.ID))
				return nil
			}

			// The join did not take. Report it and back off rather than
			// falling back to remove-then-rejoin: that path left the node out
			// of the configuration for up to 59s, and a leader change inside
			// that window orphaned it entirely.
			if err := r.setVoterInPlace(n.ID, true); err != nil {
				r.logger.Warn("Failed to promote non-voter",
					zap.String("node_id", n.ID),
					zap.Error(err))
				reconciler.mu.Lock()
				reconciler.cooldowns[n.ID] = time.Now().Add(voterChangeCooldown)
				reconciler.mu.Unlock()
				return err
			}

			r.logger.Info("Successfully promoted non-voter to voter",
				zap.String("node_id", n.ID))
			return nil
		}
	}

	return nil
}

// setVoterInPlace changes a member's voter status without taking it out of the
// raft configuration.
//
// It replaces a remove-then-rejoin sequence that left the node outside the
// configuration for up to 59 seconds while it retried the join with backoff. A
// leader change anywhere in that window orphaned the node — the code said so
// itself, in a comment on the rollback path that admitted the rollback could
// fail too. rqlite takes a voter flag on POST /join for an existing member, so
// the configuration change is one committed entry with no window at all.
//
// The quorum guard is kept and narrowed. It refused to act whenever ANY member
// was unreachable, which meant a cluster carrying one dead node could never
// correct its voter set; what matters is that a quorum can commit the change
// and that demoting this particular node does not cost the cluster its quorum.
func (r *RQLiteManager) setVoterInPlace(nodeID string, voter bool) error {
	if !voter {
		nodes, err := r.getAllClusterNodes()
		if err != nil {
			return fmt.Errorf("quorum pre-check: %w", err)
		}

		reachableVoters, totalVoters := 0, 0
		targetReachable := false
		for _, n := range nodes {
			if n.Voter {
				totalVoters++
				if n.Reachable {
					reachableVoters++
				}
			}
			if n.ID == nodeID && n.Reachable {
				targetReachable = true
			}
		}

		// Demoting a node that is not answering is not a demotion, it is an
		// eviction by another name, and it goes through evictDeadVoters where
		// the evidence bar is much higher.
		if !targetReachable {
			return fmt.Errorf("target node %s is not reachable; a dead voter is evicted, not demoted", nodeID)
		}

		// After the demotion there is one fewer voter, and this node was one of
		// the reachable ones.
		votersAfter := totalVoters - 1
		reachableAfter := reachableVoters - 1
		if votersAfter < 1 {
			return fmt.Errorf("cannot demote the last voter")
		}
		if quorum := votersAfter/2 + 1; reachableAfter < quorum {
			return fmt.Errorf("cannot demote %s: %d of %d voters would remain reachable, short of the %d needed",
				nodeID, reachableAfter, votersAfter, quorum)
		}
	}

	if err := r.joinClusterNode(nodeID, nodeID, voter); err != nil {
		return fmt.Errorf("set voter=%v for %s: %w", voter, nodeID, err)
	}
	return nil
}

// decodeNodes reads an rqlite /nodes response in either the ver=2 wrapped shape
// or the plain array. Split out so the shapes can be tested without a server.
func decodeNodes(body []byte) (RQLiteNodes, error) {
	var wrapped struct {
		Nodes RQLiteNodes `json:"nodes"`
	}
	if err := json.Unmarshal(body, &wrapped); err == nil && wrapped.Nodes != nil {
		return wrapped.Nodes, nil
	}

	var nodes RQLiteNodes
	if err := json.Unmarshal(body, &nodes); err != nil {
		return nil, fmt.Errorf("parse nodes: %w", err)
	}
	return nodes, nil
}

// getAllClusterNodes queries /nodes?nonvoters&ver=2 to get all cluster members
// including non-voters.
func (r *RQLiteManager) getAllClusterNodes() (RQLiteNodes, error) {
	url := fmt.Sprintf("http://localhost:%d/nodes?nonvoters&ver=2&timeout=5s", r.config.RQLitePort)
	client := &http.Client{Timeout: 10 * time.Second}

	resp, err := client.Get(url)
	if err != nil {
		return nil, fmt.Errorf("query nodes: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("nodes returned %d: %s", resp.StatusCode, string(body))
	}

	return decodeNodes(body)
}

// removeClusterNode sends DELETE /remove to remove a node from the Raft cluster.
func (r *RQLiteManager) removeClusterNode(nodeID string) error {
	url := fmt.Sprintf("http://localhost:%d/remove", r.config.RQLitePort)
	payload, _ := json.Marshal(map[string]string{"id": nodeID})

	req, err := http.NewRequest(http.MethodDelete, url, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("remove request: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("remove returned %d: %s", resp.StatusCode, string(body))
	}
	return nil
}

// joinClusterNode sends POST /join to add a node to the Raft cluster
// with the specified voter status.
func (r *RQLiteManager) joinClusterNode(nodeID, raftAddr string, voter bool) error {
	url := fmt.Sprintf("http://localhost:%d/join", r.config.RQLitePort)
	payload, _ := json.Marshal(map[string]interface{}{
		"id":    nodeID,
		"addr":  raftAddr,
		"voter": voter,
	})

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Post(url, "application/json", bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("join request: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("join returned %d: %s", resp.StatusCode, string(body))
	}
	return nil
}
