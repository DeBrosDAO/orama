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

// voterReconciler holds voter change cooldown state.
type voterReconciler struct {
	mu        sync.Mutex
	cooldowns map[string]time.Time // nodeID → earliest next attempt
}

// startVoterReconciliation periodically checks and corrects voter/non-voter
// assignments. Only takes effect on the leader node. Corrects at most one
// node per cycle to minimize disruption.
func (r *RQLiteManager) startVoterReconciliation(ctx context.Context) {
	reconciler := &voterReconciler{
		cooldowns: make(map[string]time.Time),
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
			if err := r.reconcileVoters(reconciler); err != nil {
				r.logger.Debug("Voter reconciliation skipped", zap.Error(err))
			}
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
			r.recoverOrphanedNodes()
		}
	}
}

// recoverOrphanedNodes finds nodes known to discovery but missing from the
// Raft cluster and re-adds them.
func (r *RQLiteManager) recoverOrphanedNodes() {
	if r.discoveryService == nil {
		return
	}

	// Only the leader runs orphan recovery
	status, err := r.getRQLiteStatus()
	if err != nil || status.Store.Raft.State != "Leader" {
		return
	}

	// Get all Raft cluster members
	raftNodes, err := r.getAllClusterNodes()
	if err != nil {
		return
	}
	raftNodeSet := make(map[string]bool, len(raftNodes))
	for _, n := range raftNodes {
		raftNodeSet[n.ID] = true
	}

	// Get all discovery peers
	discoveryPeers := r.discoveryService.GetAllPeers()

	for _, peer := range discoveryPeers {
		if peer.RaftAddress == r.discoverConfig.RaftAdvAddress {
			continue // skip self
		}
		if raftNodeSet[peer.RaftAddress] {
			continue // already in cluster
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

		if err := r.joinClusterNode(peer.RaftAddress, peer.RaftAddress, shouldBeVoter); err != nil {
			r.logger.Error("Failed to re-add orphaned node",
				zap.String("node", peer.RaftAddress),
				zap.Bool("voter", shouldBeVoter),
				zap.Error(err))
		} else {
			r.logger.Info("Successfully re-added orphaned node to Raft cluster",
				zap.String("node", peer.RaftAddress),
				zap.Bool("voter", shouldBeVoter))
		}
	}
}

// reconcileVoters compares actual cluster voter assignments (from RQLite's
// /nodes endpoint) against the deterministic desired set (computeVoterSet)
// and corrects mismatches.
//
// Improvements over original:
//   - Promotion: tries direct POST /join with voter=true first (no remove needed)
//   - Leader stability: verifies leader is stable before demotion
//   - Cooldown: skips nodes that recently failed a voter change
//   - Fixes at most one node per cycle
func (r *RQLiteManager) reconcileVoters(reconciler *voterReconciler) error {
	// 1. Only the leader reconciles
	status, err := r.getRQLiteStatus()
	if err != nil {
		return fmt.Errorf("get status: %w", err)
	}
	if status.Store.Raft.State != "Leader" {
		return nil
	}

	// 2. Get all cluster nodes including non-voters
	nodes, err := r.getAllClusterNodes()
	if err != nil {
		return fmt.Errorf("get all nodes: %w", err)
	}

	if len(nodes) <= MaxDefaultVoters {
		return nil // Small cluster — all nodes should be voters
	}

	// 3. Only reconcile when every node is reachable (stable cluster)
	for _, n := range nodes {
		if !n.Reachable {
			return nil
		}
	}

	// 4. Leader stability: verify term hasn't changed recently
	// (Re-check status to confirm we're still the stable leader)
	status2, err := r.getRQLiteStatus()
	if err != nil || status2.Store.Raft.State != "Leader" || status2.Store.Raft.Term != status.Store.Raft.Term {
		return fmt.Errorf("leader state changed during reconciliation check")
	}

	// 5. Compute desired voter set from raft addresses
	raftAddrs := make([]string, 0, len(nodes))
	for _, n := range nodes {
		raftAddrs = append(raftAddrs, n.ID)
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
		_, shouldBeVoter := desiredVoters[n.ID]

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

			if err := r.changeNodeVoterStatus(n.ID, false); err != nil {
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

			// Direct join didn't change voter status, fall back to remove+rejoin
			r.logger.Info("Direct promotion didn't work, trying remove+rejoin",
				zap.String("node_id", n.ID))

			if err := r.changeNodeVoterStatus(n.ID, true); err != nil {
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

// changeNodeVoterStatus changes a node's voter status by removing it from the
// cluster and immediately re-adding it with the desired voter flag.
//
// Safety improvements:
//   - Pre-check: verify quorum would survive the temporary removal
//   - Pre-check: verify target node is still reachable
//   - Rollback: if rejoin fails, attempt to re-add with original status
//   - Retry: 5 attempts with exponential backoff (2s, 4s, 8s, 15s, 30s)
func (r *RQLiteManager) changeNodeVoterStatus(nodeID string, voter bool) error {
	// Pre-check: if demoting a voter, verify quorum safety
	if !voter {
		nodes, err := r.getAllClusterNodes()
		if err != nil {
			return fmt.Errorf("quorum pre-check: %w", err)
		}
		voterCount := 0
		targetReachable := false
		for _, n := range nodes {
			if n.Voter && n.Reachable {
				voterCount++
			}
			if n.ID == nodeID && n.Reachable {
				targetReachable = true
			}
		}
		if !targetReachable {
			return fmt.Errorf("target node %s is not reachable, skipping voter change", nodeID)
		}
		// After removing this voter, we need (voterCount-1)/2 + 1 for quorum
		if voterCount <= 2 {
			return fmt.Errorf("cannot remove voter: only %d reachable voters, quorum would be lost", voterCount)
		}
	}

	// Fresh quorum check immediately before removal
	nodes, err := r.getAllClusterNodes()
	if err != nil {
		return fmt.Errorf("fresh quorum check: %w", err)
	}
	for _, n := range nodes {
		if !n.Reachable {
			return fmt.Errorf("node %s is unreachable, aborting voter change", n.ID)
		}
	}

	// Step 1: Remove the node from the cluster
	if err := r.removeClusterNode(nodeID); err != nil {
		return fmt.Errorf("remove node: %w", err)
	}

	// Wait for Raft to commit the configuration change, then rejoin with retries
	// Exponential backoff: 2s, 4s, 8s, 15s, 30s
	backoffs := []time.Duration{2 * time.Second, 4 * time.Second, 8 * time.Second, 15 * time.Second, 30 * time.Second}
	var lastErr error
	for attempt, wait := range backoffs {
		time.Sleep(wait)

		if err := r.joinClusterNode(nodeID, nodeID, voter); err != nil {
			lastErr = err
			r.logger.Warn("Rejoin attempt failed, retrying",
				zap.String("node_id", nodeID),
				zap.Int("attempt", attempt+1),
				zap.Int("max_attempts", len(backoffs)),
				zap.Error(err))
			continue
		}
		return nil // Success
	}

	// All rejoin attempts failed — try to re-add with the ORIGINAL status as rollback
	r.logger.Error("All rejoin attempts failed, attempting rollback",
		zap.String("node_id", nodeID),
		zap.Bool("desired_voter", voter),
		zap.Error(lastErr))

	originalVoter := !voter
	if err := r.joinClusterNode(nodeID, nodeID, originalVoter); err != nil {
		r.logger.Error("Rollback also failed — node may be orphaned (orphan recovery will re-add it)",
			zap.String("node_id", nodeID),
			zap.Error(err))
	}

	return fmt.Errorf("rejoin node after %d attempts: %w", len(backoffs), lastErr)
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

	// Try ver=2 wrapped format first
	var wrapped struct {
		Nodes RQLiteNodes `json:"nodes"`
	}
	if err := json.Unmarshal(body, &wrapped); err == nil && wrapped.Nodes != nil {
		return wrapped.Nodes, nil
	}

	// Fall back to plain array
	var nodes RQLiteNodes
	if err := json.Unmarshal(body, &nodes); err != nil {
		return nil, fmt.Errorf("parse nodes: %w", err)
	}
	return nodes, nil
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
