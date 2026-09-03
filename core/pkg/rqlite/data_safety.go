package rqlite

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"go.uber.org/zap"
)

// getRaftLogIndex returns the current Raft log index for this node
// It first tries to get the index from the running RQLite instance via /status endpoint.
// If that fails or returns 0, it falls back to reading persisted snapshot metadata from disk.
// This ensures accurate log index reporting even before RQLite is fully started.
func (r *RQLiteManager) getRaftLogIndex() (uint64, bool) {
	status, err := r.getRQLiteStatus()
	if err == nil {
		// Return the highest index we have from runtime status
		maxIndex := status.Store.Raft.LastLogIndex
		if status.Store.Raft.AppliedIndex > maxIndex {
			maxIndex = status.Store.Raft.AppliedIndex
		}
		if status.Store.Raft.CommitIndex > maxIndex {
			maxIndex = status.Store.Raft.CommitIndex
		}

		// If runtime status reports a valid index, use it
		if maxIndex > 0 {
			return maxIndex, true
		}

		// Runtime status returned 0, fall back to persisted snapshot metadata
		// This handles the case where RQLite is running but hasn't applied any logs yet
		persisted, known := r.getPersistedRaftLogIndex()
		if persisted > 0 {
			r.logger.Debug("Using persisted Raft log index because runtime status reported zero",
				zap.Uint64("persisted_index", persisted))
			return persisted, true
		}
		// A live node reporting zero with a readable (or absent) snapshot
		// directory genuinely has no data. An unreadable one is unknown.
		return 0, known
	}

	// RQLite status endpoint is not available (not started yet or unreachable)
	// Fall back to reading persisted snapshot metadata from disk
	persisted, known := r.getPersistedRaftLogIndex()
	if persisted > 0 {
		r.logger.Debug("Using persisted Raft log index before RQLite is reachable",
			zap.Uint64("persisted_index", persisted),
			zap.Error(err))
		return persisted, true
	}

	// rqlite is unreachable AND there is no snapshot to read. That is
	// indistinguishable from a node that has never held data, so it is only a
	// trustworthy zero when the snapshot directory was genuinely absent.
	r.logger.Debug("Failed to get Raft log index", zap.Error(err), zap.Bool("known", known))
	return 0, known
}

// getPersistedRaftLogIndex reads the highest Raft log index from snapshot
// metadata, and reports whether the answer is KNOWN.
//
// The distinction is the whole point. Every error used to become a zero, and a
// caller reads a zero as "this node has no data" and deletes its raft log. An
// unreadable meta.json, a permissions problem on rsnapshots — any of them could
// destroy the only good copy of the cluster's state.
func (r *RQLiteManager) getPersistedRaftLogIndex() (uint64, bool) {
	rqliteDataDir, err := r.rqliteDataDirPath()
	if err != nil {
		return 0, false
	}

	snapshotsDir := filepath.Join(rqliteDataDir, "rsnapshots")
	entries, err := os.ReadDir(snapshotsDir)
	if err != nil {
		// An ABSENT directory is a trustworthy zero: this node has taken no
		// snapshots. Any other error is not — a permissions problem or an I/O
		// failure says nothing about how much data is here, and the caller
		// destroys the raft log on a zero.
		if os.IsNotExist(err) {
			return 0, true
		}
		r.logger.Warn("Cannot read the snapshot directory, so this node's raft log index is unknown",
			zap.String("path", snapshotsDir), zap.Error(err))
		return 0, false
	}

	var maxIndex uint64
	for _, entry := range entries {
		// Only process directories (snapshot directories)
		if !entry.IsDir() {
			continue
		}

		// Read meta.json from the snapshot directory
		metaPath := filepath.Join(snapshotsDir, entry.Name(), "meta.json")
		raw, err := os.ReadFile(metaPath)
		if err != nil {
			// A snapshot whose metadata cannot be read might hold the highest
			// index there is. Continuing would report a lower one — or zero —
			// as if it were fact.
			r.logger.Warn("Cannot read snapshot metadata, so this node's raft log index is unknown",
				zap.String("path", metaPath), zap.Error(err))
			return 0, false
		}

		// Parse the metadata JSON to extract the Index field
		var meta struct {
			Index uint64 `json:"Index"`
		}
		if err := json.Unmarshal(raw, &meta); err != nil {
			r.logger.Warn("Cannot parse snapshot metadata, so this node's raft log index is unknown",
				zap.String("path", metaPath), zap.Error(err))
			return 0, false
		}

		// Track the highest index found
		if meta.Index > maxIndex {
			maxIndex = meta.Index
		}
	}

	return maxIndex, true
}

// getRQLiteStatus queries the /status endpoint for cluster information
func (r *RQLiteManager) getRQLiteStatus() (*RQLiteStatus, error) {
	url := fmt.Sprintf("http://localhost:%d/status", r.config.RQLitePort)
	client := &http.Client{Timeout: 5 * time.Second}

	resp, err := client.Get(url)
	if err != nil {
		return nil, fmt.Errorf("failed to query status: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("status endpoint returned %d: %s", resp.StatusCode, string(body))
	}

	var status RQLiteStatus
	if err := json.NewDecoder(resp.Body).Decode(&status); err != nil {
		return nil, fmt.Errorf("failed to decode status: %w", err)
	}

	return &status, nil
}

// getRQLiteNodes queries the /nodes endpoint for cluster membership
func (r *RQLiteManager) getRQLiteNodes() (RQLiteNodes, error) {
	url := fmt.Sprintf("http://localhost:%d/nodes?ver=2", r.config.RQLitePort)
	client := &http.Client{Timeout: 5 * time.Second}

	resp, err := client.Get(url)
	if err != nil {
		return nil, fmt.Errorf("failed to query nodes: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("nodes endpoint returned %d: %s", resp.StatusCode, string(body))
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read nodes response: %w", err)
	}

	// rqlite v8 wraps nodes in a top-level object; fall back to a raw array for older versions.
	var wrapped struct {
		Nodes RQLiteNodes `json:"nodes"`
	}
	if err := json.Unmarshal(body, &wrapped); err == nil && wrapped.Nodes != nil {
		return wrapped.Nodes, nil
	}

	// Try legacy format (plain array)
	var nodes RQLiteNodes
	if err := json.Unmarshal(body, &nodes); err != nil {
		return nil, fmt.Errorf("failed to decode nodes: %w", err)
	}

	return nodes, nil
}

// getRQLiteLeader returns the current leader address
func (r *RQLiteManager) getRQLiteLeader() (string, error) {
	status, err := r.getRQLiteStatus()
	if err != nil {
		return "", err
	}

	leaderAddr := status.Store.Raft.LeaderAddr
	if leaderAddr == "" {
		return "", fmt.Errorf("no leader found")
	}

	return leaderAddr, nil
}

// isNodeReachable tests if a specific node is responding
func (r *RQLiteManager) isNodeReachable(httpAddress string) bool {
	url := fmt.Sprintf("http://%s/status", httpAddress)
	client := &http.Client{Timeout: 3 * time.Second}

	resp, err := client.Get(url)
	if err != nil {
		return false
	}
	defer resp.Body.Close()

	return resp.StatusCode == http.StatusOK
}
