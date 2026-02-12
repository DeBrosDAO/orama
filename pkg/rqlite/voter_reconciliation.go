package rqlite

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"go.uber.org/zap"
)

// startVoterReconciliation periodically checks and corrects voter/non-voter
// assignments. Only takes effect on the leader node. Corrects at most one
// node per cycle to minimize disruption.
func (r *RQLiteManager) startVoterReconciliation(ctx context.Context) {
	// Wait for cluster to stabilize after startup
	time.Sleep(3 * time.Minute)

	ticker := time.NewTicker(2 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := r.reconcileVoters(); err != nil {
				r.logger.Debug("Voter reconciliation skipped", zap.Error(err))
			}
		}
	}
}

// reconcileVoters compares actual cluster voter assignments (from RQLite's
// /nodes endpoint) against the deterministic desired set (computeVoterSet)
// and corrects mismatches. Uses remove + re-join since RQLite's /join
// ignores voter flag changes for existing members.
//
// Safety: only runs on the leader, only when all nodes are reachable,
// never demotes the leader, and fixes at most one node per cycle.
func (r *RQLiteManager) reconcileVoters() error {
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

	// 4. Compute desired voter set from raft addresses
	raftAddrs := make([]string, 0, len(nodes))
	for _, n := range nodes {
		raftAddrs = append(raftAddrs, n.ID)
	}
	desiredVoters := computeVoterSet(raftAddrs, MaxDefaultVoters)

	// 5. Safety: never demote ourselves (the current leader)
	myRaftAddr := status.Store.Raft.LeaderID
	if _, shouldBeVoter := desiredVoters[myRaftAddr]; !shouldBeVoter {
		r.logger.Warn("Leader is not in computed voter set — skipping reconciliation",
			zap.String("leader_id", myRaftAddr))
		return nil
	}

	// 6. Find one mismatch to fix (one change per cycle)
	for _, n := range nodes {
		_, shouldBeVoter := desiredVoters[n.ID]

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
				return err
			}

			r.logger.Info("Successfully demoted voter to non-voter",
				zap.String("node_id", n.ID))
			return nil // One change per cycle
		}

		if !n.Voter && shouldBeVoter {
			r.logger.Info("Promoting non-voter to voter",
				zap.String("node_id", n.ID))

			if err := r.changeNodeVoterStatus(n.ID, true); err != nil {
				r.logger.Warn("Failed to promote non-voter",
					zap.String("node_id", n.ID),
					zap.Error(err))
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
// This is necessary because RQLite's /join endpoint ignores voter flag changes
// for nodes that are already cluster members with the same ID and address.
func (r *RQLiteManager) changeNodeVoterStatus(nodeID string, voter bool) error {
	// Step 1: Remove the node from the cluster
	if err := r.removeClusterNode(nodeID); err != nil {
		return fmt.Errorf("remove node: %w", err)
	}

	// Brief pause for Raft to commit the configuration change
	time.Sleep(2 * time.Second)

	// Step 2: Re-add with the correct voter status
	if err := r.joinClusterNode(nodeID, nodeID, voter); err != nil {
		return fmt.Errorf("rejoin node: %w", err)
	}

	return nil
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
