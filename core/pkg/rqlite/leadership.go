package rqlite

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"go.uber.org/zap"
)

// TransferLeadership attempts to transfer Raft leadership to another voter.
// Used by both the RQLiteManager (on Stop) and the CLI (pre-upgrade).
// Returns nil if this node is not the leader or if transfer succeeds.
func TransferLeadership(port int, logger *zap.Logger) error {
	client := &http.Client{Timeout: 5 * time.Second}

	// 1. Check if we're the leader
	statusURL := fmt.Sprintf("http://localhost:%d/status", port)
	resp, err := client.Get(statusURL)
	if err != nil {
		return fmt.Errorf("failed to query status: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read status: %w", err)
	}

	var status RQLiteStatus
	if err := json.Unmarshal(body, &status); err != nil {
		return fmt.Errorf("failed to parse status: %w", err)
	}

	if status.Store.Raft.State != "Leader" {
		logger.Debug("Not the leader, skipping transfer", zap.Int("port", port))
		return nil
	}

	logger.Info("This node is the Raft leader, attempting leadership transfer",
		zap.Int("port", port),
		zap.String("leader_id", status.Store.Raft.LeaderID))

	// 2. Find an eligible voter to transfer to
	nodesURL := fmt.Sprintf("http://localhost:%d/nodes?nonvoters&ver=2&timeout=5s", port)
	nodesResp, err := client.Get(nodesURL)
	if err != nil {
		return fmt.Errorf("failed to query nodes: %w", err)
	}
	defer nodesResp.Body.Close()

	nodesBody, err := io.ReadAll(nodesResp.Body)
	if err != nil {
		return fmt.Errorf("failed to read nodes: %w", err)
	}

	// Try ver=2 wrapped format, fall back to plain array
	var nodes RQLiteNodes
	var wrapped struct {
		Nodes RQLiteNodes `json:"nodes"`
	}
	if err := json.Unmarshal(nodesBody, &wrapped); err == nil && wrapped.Nodes != nil {
		nodes = wrapped.Nodes
	} else {
		_ = json.Unmarshal(nodesBody, &nodes)
	}

	// Find a reachable voter that is NOT us
	var targetID string
	for _, n := range nodes {
		if n.Voter && n.Reachable && n.ID != status.Store.Raft.LeaderID {
			targetID = n.ID
			break
		}
	}

	if targetID == "" {
		logger.Warn("No eligible voter found for leadership transfer — will rely on SIGTERM graceful step-down",
			zap.Int("port", port))
		return nil
	}

	// 3. Attempt transfer via rqlite v8+ API
	// POST /nodes/<target>/transfer-leadership
	// If the API doesn't exist (404), fall back to relying on SIGTERM.
	transferURL := fmt.Sprintf("http://localhost:%d/nodes/%s/transfer-leadership", port, targetID)
	transferResp, err := client.Post(transferURL, "application/json", nil)
	if err != nil {
		logger.Warn("Leadership transfer request failed, relying on SIGTERM",
			zap.Error(err))
		return nil
	}
	transferResp.Body.Close()

	if transferResp.StatusCode == http.StatusNotFound {
		logger.Info("Leadership transfer API not available (rqlite version), relying on SIGTERM")
		return nil
	}

	if transferResp.StatusCode != http.StatusOK {
		logger.Warn("Leadership transfer returned unexpected status",
			zap.Int("status", transferResp.StatusCode))
		return nil
	}

	// 4. Verify transfer
	time.Sleep(2 * time.Second)
	verifyResp, err := client.Get(statusURL)
	if err != nil {
		logger.Info("Could not verify transfer (node may have already stepped down)")
		return nil
	}
	defer verifyResp.Body.Close()

	verifyBody, _ := io.ReadAll(verifyResp.Body)
	var newStatus RQLiteStatus
	if err := json.Unmarshal(verifyBody, &newStatus); err == nil {
		if newStatus.Store.Raft.State != "Leader" {
			logger.Info("Leadership transferred successfully",
				zap.String("new_leader", newStatus.Store.Raft.LeaderID),
				zap.Int("port", port))
		} else {
			logger.Warn("Still leader after transfer attempt — will rely on SIGTERM",
				zap.Int("port", port))
		}
	}

	return nil
}
