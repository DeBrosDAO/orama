package rqlite

import (
	"encoding/json"
	"errors"
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

// ErrNoTransferTarget means this node is the leader but no other reachable
// voter could take over. Stopping now forces an election with no obvious
// successor, so callers should treat it as a refusal rather than a warning.
var ErrNoTransferTarget = errors.New("no eligible voter to transfer leadership to")

// TransferLeadership attempts to transfer Raft leadership to another voter.
// Used by both the RQLiteManager (on Stop) and the CLI (pre-upgrade).
//
// Returns nil when this node is not the leader, or when leadership has
// demonstrably moved. An error means this node is STILL the leader, which is
// the caller's cue not to stop it.
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
		return ErrNoTransferTarget
	}
	return TransferLeadershipTo(port, targetID, logger)
}

// TransferLeadershipTo transfers Raft leadership to a SPECIFIC target node ID
// (its raft address). The caller is responsible for confirming this node is the
// leader and that targetID is an eligible voter.
//
// It returns nil only once this node has actually stopped being the leader.
// Every failure used to be logged and swallowed, so a caller could not tell a
// completed handover from a leader that never moved — and the one caller that
// mattered, the pre-upgrade step, printed a warning and restarted the leader
// anyway. A 404 is the exception: it means the rqlite build has no
// transfer-leadership API, which is a capability gap rather than a failure, and
// the caller falls back to SIGTERM step-down.
func TransferLeadershipTo(port int, targetID string, logger *zap.Logger) error {
	client := &http.Client{Timeout: 5 * time.Second}

	logger.Info("Attempting Raft leadership transfer",
		zap.Int("port", port), zap.String("target", targetID))

	transferURL := fmt.Sprintf("http://localhost:%d/nodes/%s/transfer-leadership", port, targetID)
	transferResp, err := client.Post(transferURL, "application/json", nil)
	if err != nil {
		return fmt.Errorf("leadership transfer request to %s: %w", targetID, err)
	}
	transferResp.Body.Close()

	switch {
	case transferResp.StatusCode == http.StatusNotFound:
		logger.Info("Leadership transfer API not available (rqlite version); relying on SIGTERM step-down",
			zap.Int("port", port))
		return nil
	case transferResp.StatusCode != http.StatusOK:
		return fmt.Errorf("leadership transfer to %s returned HTTP %d", targetID, transferResp.StatusCode)
	}

	// Confirm against the real signal. The POST only starts the handover; raft
	// still has to elect the target, so returning here would report success on
	// a node that is about to be killed while still leading.
	if err := waitForStepDown(port, transferStepDownTimeout); err != nil {
		return err
	}
	logger.Info("Leadership transferred", zap.String("target", targetID), zap.Int("port", port))
	return nil
}

// transferStepDownTimeout bounds the wait for raft to elect the transfer
// target. Generous relative to the 1s election timeout so a slow overlay link
// does not look like a failed handover. A var so tests can shorten it.
var transferStepDownTimeout = 15 * time.Second

// waitForStepDown blocks until this node reports a state other than Leader.
func waitForStepDown(port int, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	var lastState string
	for {
		status, err := GetRaftStatus(port)
		if err == nil {
			lastState = status.Store.Raft.State
			if lastState != "Leader" {
				return nil
			}
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("still leader %s after transfer (last observed state %q)", timeout, lastState)
		}
		time.Sleep(500 * time.Millisecond)
	}
}
