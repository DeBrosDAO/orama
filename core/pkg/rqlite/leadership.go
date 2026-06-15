package rqlite

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"go.uber.org/zap"
)

// GetRaftStatus queries a local rqlite node's /status endpoint.
func GetRaftStatus(port int) (*RQLiteStatus, error) {
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(fmt.Sprintf("http://localhost:%d/status", port))
	if err != nil {
		return nil, fmt.Errorf("failed to query status: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read status: %w", err)
	}
	var status RQLiteStatus
	if err := json.Unmarshal(body, &status); err != nil {
		return nil, fmt.Errorf("failed to parse status: %w", err)
	}
	return &status, nil
}

// GetRaftNodes queries a local rqlite node's /nodes endpoint (voters +
// non-voters, with reachability).
func GetRaftNodes(port int) (RQLiteNodes, error) {
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(fmt.Sprintf("http://localhost:%d/nodes?nonvoters&ver=2&timeout=5s", port))
	if err != nil {
		return nil, fmt.Errorf("failed to query nodes: %w", err)
	}
	defer resp.Body.Close()
	nodesBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read nodes: %w", err)
	}
	// Try ver=2 wrapped format, fall back to plain array.
	var nodes RQLiteNodes
	var wrapped struct {
		Nodes RQLiteNodes `json:"nodes"`
	}
	if err := json.Unmarshal(nodesBody, &wrapped); err == nil && wrapped.Nodes != nil {
		nodes = wrapped.Nodes
	} else {
		_ = json.Unmarshal(nodesBody, &nodes)
	}
	return nodes, nil
}

// TransferLeadership attempts to transfer Raft leadership to another voter.
// Used by both the RQLiteManager (on Stop) and the CLI (pre-upgrade).
// Returns nil if this node is not the leader or if transfer succeeds.
func TransferLeadership(port int, logger *zap.Logger) error {
	status, err := GetRaftStatus(port)
	if err != nil {
		return err
	}
	if status.Store.Raft.State != "Leader" {
		logger.Debug("Not the leader, skipping transfer", zap.Int("port", port))
		return nil
	}

	nodes, err := GetRaftNodes(port)
	if err != nil {
		return err
	}

	// Find any reachable voter that is NOT us.
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
	return TransferLeadershipTo(port, targetID, logger)
}

// TransferLeadershipTo transfers Raft leadership to a SPECIFIC target node ID
// (its raft address). The caller is responsible for confirming this node is the
// leader and that targetID is an eligible voter. Tolerant of a missing API
// (404) and a non-OK status — it logs and returns nil so callers treat transfer
// as best-effort.
func TransferLeadershipTo(port int, targetID string, logger *zap.Logger) error {
	client := &http.Client{Timeout: 5 * time.Second}

	logger.Info("Attempting Raft leadership transfer",
		zap.Int("port", port), zap.String("target", targetID))

	transferURL := fmt.Sprintf("http://localhost:%d/nodes/%s/transfer-leadership", port, targetID)
	transferResp, err := client.Post(transferURL, "application/json", nil)
	if err != nil {
		logger.Warn("Leadership transfer request failed", zap.Error(err))
		return nil
	}
	transferResp.Body.Close()

	switch {
	case transferResp.StatusCode == http.StatusNotFound:
		logger.Info("Leadership transfer API not available (rqlite version)")
		return nil
	case transferResp.StatusCode != http.StatusOK:
		logger.Warn("Leadership transfer returned unexpected status",
			zap.Int("status", transferResp.StatusCode))
		return nil
	}

	// Verify.
	time.Sleep(2 * time.Second)
	newStatus, err := GetRaftStatus(port)
	if err != nil {
		logger.Info("Could not verify transfer (node may have already stepped down)")
		return nil
	}
	if newStatus.Store.Raft.State != "Leader" {
		logger.Info("Leadership transferred successfully",
			zap.String("new_leader", newStatus.Store.Raft.LeaderID), zap.Int("port", port))
	} else {
		logger.Warn("Still leader after transfer attempt", zap.Int("port", port))
	}
	return nil
}
