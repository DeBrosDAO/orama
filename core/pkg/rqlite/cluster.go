package rqlite

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"go.uber.org/zap"
)

// minClusterSizeWait bounds waitForMinClusterSizeBeforeStart. Past it the node
// starts anyway: waiting longer for peers that may never come cannot make the
// cluster larger, and a node that is up and retrying its join is strictly more
// useful than one that is still blocked in start-up.
const minClusterSizeWait = 2 * time.Minute

// waitForMinClusterSizeBeforeStart waits for the configured minimum number of
// peers to be discovered, up to minClusterSizeWait. Falling through the
// deadline is not an error — see the constant.
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

	if err := r.discoveryService.TriggerPeerExchange(ctx); err != nil {
		r.logger.Warn("Failed to trigger peer exchange before cluster wait", zap.Error(err))
	}

	deadline := time.Now().Add(minClusterSizeWait)
	checkInterval := 2 * time.Second

	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		if time.Now().After(deadline) {
			r.logger.Warn("Starting without the configured minimum cluster size — continuing so the node can serve locally and keep retrying its join",
				zap.Int("min_cluster_size", r.config.MinClusterSize),
				zap.Duration("waited", minClusterSizeWait))
			return nil
		}

		r.discoveryService.TriggerSync()

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(checkInterval):
		}

		allPeers := r.discoveryService.GetAllPeers()
		remotePeerCount := 0
		for _, peer := range allPeers {
			if peer.NodeID != r.discoverConfig.RaftAdvAddress {
				remotePeerCount++
			}
		}

		if remotePeerCount < requiredRemotePeers {
			continue
		}

		// Check discovery-peers.json (safe location outside raft dir)
		peersPath := filepath.Join(rqliteDataDir, "discovery-peers.json")
		r.discoveryService.TriggerSync()

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}

		info, err := os.Stat(peersPath)
		if err != nil || info.Size() <= 10 {
			continue
		}
		data, err := os.ReadFile(peersPath)
		if err != nil {
			continue
		}
		var peers []map[string]interface{}
		if err := json.Unmarshal(data, &peers); err == nil && len(peers) >= requiredRemotePeers {
			return nil
		}
	}
}

// performPreStartClusterDiscovery builds peers.json before starting RQLite
func (r *RQLiteManager) performPreStartClusterDiscovery(ctx context.Context, rqliteDataDir string) error {
	if r.discoveryService == nil {
		return fmt.Errorf("discovery service not available")
	}

	if err := r.discoveryService.TriggerPeerExchange(ctx); err != nil {
		r.logger.Warn("Failed to trigger peer exchange during pre-start discovery", zap.Error(err))
	}
	r.discoveryService.TriggerSync()
	time.Sleep(500 * time.Millisecond)

	// Wait up to 45s for peer discovery — parallel dials compensate for the shorter deadline
	discoveryDeadline := time.Now().Add(45 * time.Second)
	var discoveredPeers int

	for time.Now().Before(discoveryDeadline) {
		allPeers := r.discoveryService.GetAllPeers()
		discoveredPeers = len(allPeers)

		if discoveredPeers >= r.config.MinClusterSize {
			r.logger.Info("Discovered required peers for cluster",
				zap.Int("discovered", discoveredPeers),
				zap.Int("required", r.config.MinClusterSize))
			break
		}
		time.Sleep(2 * time.Second)
	}

	// If we only discovered ourselves, do NOT write a single-node peers.json.
	// Writing single-node peers.json causes RQLite to bootstrap as a solo cluster,
	// making it impossible to rejoin the actual cluster later (-join fails with
	// "single-node cluster, joining not supported"). Let RQLite start with its
	// existing Raft state or use the -join flag to connect.
	if discoveredPeers <= 1 {
		r.logger.Warn("Only discovered self during pre-start discovery, skipping peers.json write to prevent solo bootstrap",
			zap.Int("discovered_peers", discoveredPeers),
			zap.Int("min_cluster_size", r.config.MinClusterSize))
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
			if err := r.clearRaftState(rqliteDataDir); err != nil {
				r.logger.Warn("Failed to clear raft state during pre-start discovery", zap.Error(err))
			}
			if err := r.discoveryService.ForceWritePeersJSON(); err != nil {
				r.logger.Warn("Failed to write peers.json after clearing raft state", zap.Error(err))
			}
		}
	}

	r.discoveryService.TriggerSync()
	time.Sleep(500 * time.Millisecond)

	return nil
}

// recoverCluster restarts RQLite using peers.json
func (r *RQLiteManager) recoverCluster(ctx context.Context, peersJSONPath string) error {
	// shutdown, not Stop: Stop is once-only (it is also reached through
	// RQLiteAdapter.Close on the shutdown path), and a recovery must actually
	// stop the instance every time it runs.
	if err := r.shutdown(); err != nil {
		r.logger.Warn("Failed to stop RQLite during cluster recovery", zap.Error(err))
	}
	time.Sleep(2 * time.Second)

	cmd := exec.Command("systemctl", "restart", "orama-namespace-rqlite@index.service")
	if os.Getuid() != 0 {
		cmd = exec.Command("sudo", "systemctl", "restart", "orama-namespace-rqlite@index.service")
	}
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("restart orama-namespace-rqlite@index: %w (%s)", err, out)
	}

	return r.waitForReadyAndConnect(ctx)
}

// recoverFromSplitBrain automatically recovers from split-brain state
func (r *RQLiteManager) recoverFromSplitBrain(ctx context.Context) error {
	if r.discoveryService == nil {
		return fmt.Errorf("discovery service not available")
	}

	r.discoveryService.TriggerPeerExchange(ctx)
	r.discoveryService.TriggerSync()
	time.Sleep(500 * time.Millisecond)

	rqliteDataDir, _ := r.rqliteDataDirPath()
	ourIndex := r.getRaftLogIndex()

	maxPeerIndex := uint64(0)
	for _, peer := range r.discoveryService.GetAllPeers() {
		if peer.NodeID != r.discoverConfig.RaftAdvAddress && peer.RaftLogIndex > maxPeerIndex {
			maxPeerIndex = peer.RaftLogIndex
		}
	}

	if ourIndex == 0 && maxPeerIndex > 0 {
		if err := r.clearRaftState(rqliteDataDir); err != nil {
			r.logger.Warn("Failed to clear raft state during split-brain recovery", zap.Error(err))
		}
		r.discoveryService.TriggerPeerExchange(ctx)
		time.Sleep(500 * time.Millisecond)
		if err := r.discoveryService.ForceWritePeersJSON(); err != nil {
			r.logger.Warn("Failed to write peers.json during split-brain recovery", zap.Error(err))
		}
		return r.recoverCluster(ctx, filepath.Join(rqliteDataDir, "raft", "peers.json"))
	}

	return nil
}

// splitBrainSettleDelay is how long the split-brain detector waits before its
// first check, so a node that is simply still converging is not mistaken for
// one that has bootstrapped alone.
const splitBrainSettleDelay = 30 * time.Second

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
	select {
	case <-ctx.Done():
		return
	case <-time.After(splitBrainSettleDelay):
	}

	ticker := time.NewTicker(60 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if r.isInSplitBrainState() {
				if err := r.recoverFromSplitBrain(ctx); err != nil {
					r.logger.Warn("Split-brain recovery attempt failed", zap.Error(err))
				}
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
	// Don't check peers.json — discovery-peers.json is now written outside
	// the raft dir and should not be treated as existing Raft state.
	return false
}

func (r *RQLiteManager) clearRaftState(rqliteDataDir string) error {
	_ = os.Remove(filepath.Join(rqliteDataDir, "raft.db"))
	_ = os.Remove(filepath.Join(rqliteDataDir, "raft", "peers.json"))
	_ = os.Remove(filepath.Join(rqliteDataDir, "discovery-peers.json"))
	return nil
}
