package rqlite

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"go.uber.org/zap"
)

// getRaftLogIndex returns the current Raft log index for this node
func (r *RQLiteManager) getRaftLogIndex() uint64 {
	status, err := r.getRQLiteStatus()
	if err != nil {
		r.logger.Debug("Failed to get Raft log index", zap.Error(err))
		return 0
	}
	
	// Return the highest index we have
	maxIndex := status.Store.Raft.LastLogIndex
	if status.Store.Raft.AppliedIndex > maxIndex {
		maxIndex = status.Store.Raft.AppliedIndex
	}
	if status.Store.Raft.CommitIndex > maxIndex {
		maxIndex = status.Store.Raft.CommitIndex
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
	
	var nodes RQLiteNodes
	if err := json.NewDecoder(resp.Body).Decode(&nodes); err != nil {
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

