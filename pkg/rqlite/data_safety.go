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
func (r *RQLiteManager) getRaftLogIndex() uint64 {
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
			return maxIndex
		}

		// Runtime status returned 0, fall back to persisted snapshot metadata
		// This handles the case where RQLite is running but hasn't applied any logs yet
		if persisted := r.getPersistedRaftLogIndex(); persisted > 0 {
			r.logger.Debug("Using persisted Raft log index because runtime status reported zero",
				zap.Uint64("persisted_index", persisted))
			return persisted
		}
		return 0
	}

	// RQLite status endpoint is not available (not started yet or unreachable)
	// Fall back to reading persisted snapshot metadata from disk
	persisted := r.getPersistedRaftLogIndex()
	if persisted > 0 {
		r.logger.Debug("Using persisted Raft log index before RQLite is reachable",
			zap.Uint64("persisted_index", persisted),
			zap.Error(err))
		return persisted
	}

	r.logger.Debug("Failed to get Raft log index", zap.Error(err))
	return 0
}

// getPersistedRaftLogIndex reads the highest Raft log index from snapshot metadata files
// This allows us to report accurate log indexes even before RQLite is started
func (r *RQLiteManager) getPersistedRaftLogIndex() uint64 {
	rqliteDataDir, err := r.rqliteDataDirPath()
	if err != nil {
		return 0
	}

	snapshotsDir := filepath.Join(rqliteDataDir, "rsnapshots")
	entries, err := os.ReadDir(snapshotsDir)
	if err != nil {
		return 0
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
			continue
		}

		// Parse the metadata JSON to extract the Index field
		var meta struct {
			Index uint64 `json:"Index"`
		}
		if err := json.Unmarshal(raw, &meta); err != nil {
			continue
		}

		// Track the highest index found
		if meta.Index > maxIndex {
			maxIndex = meta.Index
		}
	}

	return maxIndex
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
