package rqlite

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/DeBrosOfficial/network/pkg/tlsutil"
	"go.uber.org/zap"
)

// GetRaftStatus queries an rqlite node's /status endpoint.
//
// Package-level because CLI callers use it without a manager.
func GetRaftStatus(ep Endpoint) (*RQLiteStatus, error) {
	return ep.Admin().Status(context.Background())
}

// GetRaftNodes queries an rqlite node's /nodes endpoint (voters + non-voters,
// with reachability).
func GetRaftNodes(ep Endpoint) (RQLiteNodes, error) {
	return ep.Admin().Nodes(context.Background())
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
func TransferLeadership(ep Endpoint, logger *zap.Logger) error {
	status, err := GetRaftStatus(ep)
	if err != nil {
		return err
	}
	if status.Store.Raft.State != "Leader" {
		logger.Debug("Not the leader, skipping transfer", zap.Stringer("rqlite", ep))
		return nil
	}

	nodes, err := GetRaftNodes(ep)
	if err != nil {
		return err
	}

	// Find any reachable voter that is NOT us.
	var targetID string
	for _, n := range nodes {
		if n.Voter && n.Reachable && n.ID != status.RaftLeaderID() {
			targetID = n.ID
			break
		}
	}
	if targetID == "" {
		return ErrNoTransferTarget
	}
	return TransferLeadershipTo(ep, targetID, logger)
}

// TransferLeadershipTo transfers Raft leadership to a SPECIFIC target node ID
// (its raft address). The caller is responsible for confirming this node is the
// leader and that targetID is an eligible voter.
//
// It returns nil only once this node has actually stopped being the leader.
// Every failure used to be logged and swallowed, so a caller could not tell a
// completed handover from a leader that never moved — and the one caller that
// mattered, the pre-upgrade step, printed a warning and restarted the leader
// anyway. A 404 is the exception: it means this rqlite build has no /leader
// step-down, which is a capability gap rather than a failure, and the
// caller falls back to SIGTERM step-down.
//
// This keeps its own request rather than going through AdminClient because it
// needs the raw status code. AdminClient collapses every non-2xx into one
// error. rqlite 8 steps down on POST /leader, and the target id is a JSON
// body. The query string is ignored, and POST /nodes/<id>/transfer-leadership
// is not that route: /nodes is GET-only and answers 405.
func TransferLeadershipTo(ep Endpoint, targetID string, logger *zap.Logger) error {
	client := tlsutil.NewHTTPClient(5 * time.Second)

	logger.Info("Attempting Raft leadership transfer",
		zap.Stringer("rqlite", ep), zap.String("target", targetID))

	body, err := json.Marshal(map[string]string{"id": targetID})
	if err != nil {
		return fmt.Errorf("encode leadership transfer: %w", err)
	}
	req, err := http.NewRequest(http.MethodPost, ep.BaseURL()+"/leader", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build leadership transfer request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.SetBasicAuth(ep.Username, ep.Password)
	// A fresh connection: the client's pool is shared, and Go does not retry
	// a POST on a pooled connection the server closed in the meantime — the
	// hand-over before a node stops would fail on a stale socket.
	req.Close = true

	transferResp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("leadership transfer request to %s: %w", targetID, err)
	}
	transferResp.Body.Close()

	switch {
	case transferResp.StatusCode == http.StatusNotFound:
		logger.Info("Leadership transfer API not available (rqlite version); relying on SIGTERM step-down",
			zap.Stringer("rqlite", ep))
		return nil
	case transferResp.StatusCode != http.StatusOK:
		return fmt.Errorf("leadership transfer to %s returned HTTP %d", targetID, transferResp.StatusCode)
	}

	// Confirm against the real signal. The POST only starts the handover; raft
	// still has to elect the target, so returning here would report success on
	// a node that is about to be killed while still leading.
	if err := waitForStepDown(ep, transferStepDownTimeout); err != nil {
		return err
	}
	logger.Info("Leadership transferred", zap.String("target", targetID), zap.Stringer("rqlite", ep))
	return nil
}

// transferStepDownTimeout bounds the wait for raft to elect the transfer
// target. Generous relative to the 1s election timeout so a slow overlay link
// does not look like a failed handover. A var so tests can shorten it.
var transferStepDownTimeout = 15 * time.Second

// waitForStepDown blocks until this node reports a state other than Leader.
func waitForStepDown(ep Endpoint, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	var lastState string
	for {
		status, err := GetRaftStatus(ep)
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
