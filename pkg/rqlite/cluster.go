package rqlite

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// establishLeadershipOrJoin handles post-startup cluster establishment
func (r *RQLiteManager) establishLeadershipOrJoin(ctx context.Context, rqliteDataDir string) error {
	timeout := 5 * time.Minute
	if r.config.RQLiteJoinAddress == "" {
		timeout = 2 * time.Minute
	}

	sqlCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	if err := r.waitForSQLAvailable(sqlCtx); err != nil {
		if r.cmd != nil && r.cmd.Process != nil {
			_ = r.cmd.Process.Kill()
		}
		return err
	}

	return nil
}

// waitForMinClusterSizeBeforeStart waits for minimum cluster size to be discovered
func (r *RQLiteManager) waitForMinClusterSizeBeforeStart(ctx context.Context, rqliteDataDir string) error {
	if r.discoveryService == nil {
		return fmt.Errorf("discovery service not available")
	}

	requiredRemotePeers := r.config.MinClusterSize - 1

	// Genesis node (single-node cluster) doesn't need to wait for peers
	if requiredRemotePeers <= 0 {
		r.logger.Info("Genesis node, skipping peer discovery wait")
		return nil
	}

	_ = r.discoveryService.TriggerPeerExchange(ctx)

	checkInterval := 2 * time.Second
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		r.discoveryService.TriggerSync()
		time.Sleep(checkInterval)

		allPeers := r.discoveryService.GetAllPeers()
		remotePeerCount := 0
		for _, peer := range allPeers {
			if peer.NodeID != r.discoverConfig.RaftAdvAddress {
				remotePeerCount++
			}
		}

		if remotePeerCount >= requiredRemotePeers {
			peersPath := filepath.Join(rqliteDataDir, "raft", "peers.json")
			r.discoveryService.TriggerSync()
			time.Sleep(2 * time.Second)

			if info, err := os.Stat(peersPath); err == nil && info.Size() > 10 {
				data, err := os.ReadFile(peersPath)
				if err == nil {
					var peers []map[string]interface{}
					if err := json.Unmarshal(data, &peers); err == nil && len(peers) >= requiredRemotePeers {
						return nil
					}
				}
			}
		}
	}
}

// performPreStartClusterDiscovery builds peers.json before starting RQLite
func (r *RQLiteManager) performPreStartClusterDiscovery(ctx context.Context, rqliteDataDir string) error {
	if r.discoveryService == nil {
		return fmt.Errorf("discovery service not available")
	}

	_ = r.discoveryService.TriggerPeerExchange(ctx)
	time.Sleep(1 * time.Second)
	r.discoveryService.TriggerSync()
	time.Sleep(2 * time.Second)

	discoveryDeadline := time.Now().Add(30 * time.Second)
	var discoveredPeers int

	for time.Now().Before(discoveryDeadline) {
		allPeers := r.discoveryService.GetAllPeers()
		discoveredPeers = len(allPeers)

		if discoveredPeers >= r.config.MinClusterSize {
			break
		}
		time.Sleep(2 * time.Second)
	}

	if discoveredPeers <= 1 {
		return nil
	}

	if r.hasExistingRaftState(rqliteDataDir) {
		ourLogIndex := r.getRaftLogIndex()
		maxPeerIndex := uint64(0)
		for _, peer := range r.discoveryService.GetAllPeers() {
			if peer.NodeID != r.discoverConfig.RaftAdvAddress && peer.RaftLogIndex > maxPeerIndex {
				maxPeerIndex = peer.RaftLogIndex
			}
		}

		if ourLogIndex == 0 && maxPeerIndex > 0 {
			_ = r.clearRaftState(rqliteDataDir)
			_ = r.discoveryService.ForceWritePeersJSON()
		}
	}

	r.discoveryService.TriggerSync()
	time.Sleep(2 * time.Second)

	return nil
}

// recoverCluster restarts RQLite using peers.json
func (r *RQLiteManager) recoverCluster(ctx context.Context, peersJSONPath string) error {
	_ = r.Stop()
	time.Sleep(2 * time.Second)

	rqliteDataDir, err := r.rqliteDataDirPath()
	if err != nil {
		return err
	}

	if err := r.launchProcess(ctx, rqliteDataDir); err != nil {
		return err
	}

	return r.waitForReadyAndConnect(ctx)
}

// recoverFromSplitBrain automatically recovers from split-brain state
func (r *RQLiteManager) recoverFromSplitBrain(ctx context.Context) error {
	if r.discoveryService == nil {
		return fmt.Errorf("discovery service not available")
	}

	r.discoveryService.TriggerPeerExchange(ctx)
	time.Sleep(2 * time.Second)
	r.discoveryService.TriggerSync()
	time.Sleep(2 * time.Second)

	rqliteDataDir, _ := r.rqliteDataDirPath()
	ourIndex := r.getRaftLogIndex()
	
	maxPeerIndex := uint64(0)
	for _, peer := range r.discoveryService.GetAllPeers() {
		if peer.NodeID != r.discoverConfig.RaftAdvAddress && peer.RaftLogIndex > maxPeerIndex {
			maxPeerIndex = peer.RaftLogIndex
		}
	}

	if ourIndex == 0 && maxPeerIndex > 0 {
		_ = r.clearRaftState(rqliteDataDir)
		r.discoveryService.TriggerPeerExchange(ctx)
		time.Sleep(1 * time.Second)
		_ = r.discoveryService.ForceWritePeersJSON()
		return r.recoverCluster(ctx, filepath.Join(rqliteDataDir, "raft", "peers.json"))
	}

	return nil
}

// isInSplitBrainState detects if we're in a split-brain scenario
func (r *RQLiteManager) isInSplitBrainState() bool {
	status, err := r.getRQLiteStatus()
	if err != nil || r.discoveryService == nil {
		return false
	}

	raft := status.Store.Raft
	if raft.State == "Follower" && raft.Term == 0 && raft.NumPeers == 0 && !raft.Voter {
		peers := r.discoveryService.GetActivePeers()
		if len(peers) == 0 {
			return false
		}

		reachableCount := 0
		splitBrainCount := 0
		for _, peer := range peers {
			if r.isPeerReachable(peer.HTTPAddress) {
				reachableCount++
				peerStatus, err := r.getPeerRQLiteStatus(peer.HTTPAddress)
				if err == nil {
					praft := peerStatus.Store.Raft
					if praft.State == "Follower" && praft.Term == 0 && praft.NumPeers == 0 && !praft.Voter {
						splitBrainCount++
					}
				}
			}
		}
		return reachableCount > 0 && splitBrainCount == reachableCount
	}
	return false
}

func (r *RQLiteManager) isPeerReachable(httpAddr string) bool {
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get(fmt.Sprintf("http://%s/status", httpAddr))
	if err == nil {
		resp.Body.Close()
		return resp.StatusCode == http.StatusOK
	}
	return false
}

func (r *RQLiteManager) getPeerRQLiteStatus(httpAddr string) (*RQLiteStatus, error) {
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get(fmt.Sprintf("http://%s/status", httpAddr))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var status RQLiteStatus
	if err := json.NewDecoder(resp.Body).Decode(&status); err != nil {
		return nil, err
	}
	return &status, nil
}

func (r *RQLiteManager) startHealthMonitoring(ctx context.Context) {
	time.Sleep(30 * time.Second)
	ticker := time.NewTicker(60 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if r.isInSplitBrainState() {
				_ = r.recoverFromSplitBrain(ctx)
			}
		}
	}
}

// checkNeedsClusterRecovery checks if the node has old cluster state that requires coordinated recovery
func (r *RQLiteManager) checkNeedsClusterRecovery(rqliteDataDir string) (bool, error) {
	snapshotsDir := filepath.Join(rqliteDataDir, "rsnapshots")
	if _, err := os.Stat(snapshotsDir); os.IsNotExist(err) {
		return false, nil
	}

	entries, err := os.ReadDir(snapshotsDir)
	if err != nil {
		return false, err
	}

	hasSnapshots := false
	for _, entry := range entries {
		if entry.IsDir() || strings.HasSuffix(entry.Name(), ".db") {
			hasSnapshots = true
			break
		}
	}

	if !hasSnapshots {
		return false, nil
	}

	raftLogPath := filepath.Join(rqliteDataDir, "raft.db")
	if info, err := os.Stat(raftLogPath); err == nil {
		if info.Size() <= 8*1024*1024 {
			return true, nil
		}
	}

	return false, nil
}

func (r *RQLiteManager) hasExistingRaftState(rqliteDataDir string) bool {
	raftLogPath := filepath.Join(rqliteDataDir, "raft.db")
	if info, err := os.Stat(raftLogPath); err == nil && info.Size() > 1024 {
		return true
	}
	peersPath := filepath.Join(rqliteDataDir, "raft", "peers.json")
	_, err := os.Stat(peersPath)
	return err == nil
}

func (r *RQLiteManager) clearRaftState(rqliteDataDir string) error {
	_ = os.Remove(filepath.Join(rqliteDataDir, "raft.db"))
	_ = os.Remove(filepath.Join(rqliteDataDir, "raft", "peers.json"))
	return nil
}

