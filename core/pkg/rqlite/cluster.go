package rqlite

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
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
			if !isSelfPeer(peer, r.discoverConfig.RaftAdvAddress) {
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
		ourLogIndex, known := r.getRaftLogIndex()
		maxPeerIndex := uint64(0)
		for _, peer := range r.discoveryService.GetAllPeers() {
			if !isSelfPeer(peer, r.discoverConfig.RaftAdvAddress) && peer.RaftLogIndex > maxPeerIndex {
				maxPeerIndex = peer.RaftLogIndex
			}
		}

		// A zero this node cannot vouch for is not evidence of anything. It
		// used to be treated as "we have no data" and the node destroyed its
		// own raft log on the strength of an unreadable meta.json.
		if !known {
			r.logger.Warn("Not clearing raft state: this node's log index could not be determined, " +
				"and a zero it cannot vouch for is not evidence that it holds no data")
		}

		if known && ourLogIndex == 0 && maxPeerIndex > 0 {
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
	ourIndex, known := r.getRaftLogIndex()

	maxPeerIndex := uint64(0)
	for _, peer := range r.discoveryService.GetAllPeers() {
		if !isSelfPeer(peer, r.discoverConfig.RaftAdvAddress) && peer.RaftLogIndex > maxPeerIndex {
			maxPeerIndex = peer.RaftLogIndex
		}
	}

	if !known {
		r.logger.Warn("Not clearing raft state during split-brain recovery: this node's log index " +
			"could not be determined, and a zero it cannot vouch for is not evidence that it holds no data")
	}

	if known && ourIndex == 0 && maxPeerIndex > 0 {
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

// isPeerReachable reports whether a peer's rqlite answers.
//
// Through the admin client: a peer that is up but rejects unauthenticated
// requests must not be reported unreachable, or the recovery paths act on
// evidence that a healthy node is gone.
func (r *RQLiteManager) isPeerReachable(httpAddr string) bool {
	user, pass := r.adminCredentials()
	_, err := NewAdminClient("http://"+httpAddr, user, pass).Status(context.Background())
	return err == nil
}

// getPeerRQLiteStatus reads a peer's /status, with credentials.
func (r *RQLiteManager) getPeerRQLiteStatus(httpAddr string) (*RQLiteStatus, error) {
	user, pass := r.adminCredentials()
	return NewAdminClient("http://"+httpAddr, user, pass).Status(context.Background())
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

// checkNeedsClusterRecovery reports whether this node's recorded membership is
// stale enough that it needs peers.json written before rqlited starts.
//
// The test is on MEMBERSHIP, not on file sizes. It used to return true whenever
// snapshots existed and raft.db was 8 MB or smaller — which is the normal
// steady state of a healthy node just after a snapshot compacts its log. So the
// pre-start discovery path, and the raft-state clear it can reach, ran on
// ordinary boots rather than on broken ones.
//
// Positive evidence means: this node records peers, and the set it records
// disagrees with the set discovery can currently see. A node whose recorded
// peers are simply unreachable is not stale — that is an outage, and rewriting
// its configuration during one is how a partition becomes a split brain.
func (r *RQLiteManager) checkNeedsClusterRecovery(rqliteDataDir string) (bool, error) {
	recorded, err := r.recordedPeerAddresses(rqliteDataDir)
	if err != nil {
		return false, err
	}
	if len(recorded) == 0 {
		// Nothing recorded to be stale about.
		return false, nil
	}

	if r.discoveryService == nil {
		return false, nil
	}
	discovered := map[string]bool{}
	for _, peer := range r.discoveryService.GetAllPeers() {
		if peer.RaftAddress != "" {
			discovered[peer.RaftAddress] = true
		}
	}
	if len(discovered) == 0 {
		// Discovery knows nothing yet. That is not evidence the recorded set is
		// wrong — it is evidence this node has not finished starting.
		return false, nil
	}

	// Self is always a member and never appears in discovery's peer list.
	self := r.discoverConfig.RaftAdvAddress

	for _, addr := range recorded {
		if addr == self || discovered[addr] {
			continue
		}
		r.logger.Info("Recorded raft membership disagrees with discovery; peers.json will be rewritten",
			zap.String("recorded_peer", addr),
			zap.Int("recorded", len(recorded)),
			zap.Int("discovered", len(discovered)))
		return true, nil
	}
	return false, nil
}

// recordedPeerAddresses reads the raft addresses this node has recorded as
// members, from the peers.json rqlite maintains beside its raft state.
func (r *RQLiteManager) recordedPeerAddresses(rqliteDataDir string) ([]string, error) {
	raw, err := os.ReadFile(filepath.Join(rqliteDataDir, "raft", "peers.json"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read recorded raft peers: %w", err)
	}

	var peers []struct {
		Address string `json:"address"`
	}
	if err := json.Unmarshal(raw, &peers); err != nil {
		return nil, fmt.Errorf("parse recorded raft peers: %w", err)
	}

	out := make([]string, 0, len(peers))
	for _, p := range peers {
		if p.Address != "" {
			out = append(out, p.Address)
		}
	}
	return out, nil
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

// clearRaftState sets this node's raft state aside so it starts fresh.
//
// It MOVES rather than deletes. Every caller reaches here on inferred evidence
// — "our log index is zero and a peer's is higher" — and that inference has
// been wrong before: an unreadable snapshot file used to produce the same zero
// as an empty node. Moving costs disk and keeps the only local copy of the
// cluster's state recoverable by hand; deleting costs the data.
//
// rsnapshots is included, which it was not. Removing raft.db while leaving the
// snapshots produced a node with snapshots and no log to apply them against —
// a state neither rqlite nor the next recovery attempt reasons about correctly.
func (r *RQLiteManager) clearRaftState(rqliteDataDir string) error {
	stamp := time.Now().UTC().Format("20060102T150405Z")
	discarded := filepath.Join(rqliteDataDir, fmt.Sprintf("raft.discarded-%s", stamp))

	if err := os.MkdirAll(discarded, 0o755); err != nil {
		return fmt.Errorf("create %s to set the raft state aside: %w", discarded, err)
	}

	// raft.db is the log; rsnapshots holds what it was compacted into; raft/
	// carries peers.json. All three describe one membership, so they move
	// together or the remainder is inconsistent.
	moved := 0
	for _, name := range []string{"raft.db", "rsnapshots", "raft", "discovery-peers.json"} {
		src := filepath.Join(rqliteDataDir, name)
		if _, err := os.Stat(src); err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return fmt.Errorf("inspect %s: %w", src, err)
		}
		if err := os.Rename(src, filepath.Join(discarded, name)); err != nil {
			return fmt.Errorf("set %s aside: %w", src, err)
		}
		moved++
	}

	if moved == 0 {
		// Nothing to move; do not leave an empty directory behind implying
		// something was discarded.
		_ = os.Remove(discarded)
		return nil
	}

	r.logger.Warn("Set this node's raft state aside; it will rejoin and replicate from the leader",
		zap.String("moved_to", discarded),
		zap.Int("items", moved))
	return nil
}
