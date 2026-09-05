package node

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/mackerelio/go-osstat/cpu"
	"github.com/mackerelio/go-osstat/memory"
	"go.uber.org/zap"

	"github.com/DeBrosOfficial/network/pkg/logging"
)

func logPeerStatus(n *Node, currentPeerCount int, lastPeerCount int, firstCheck bool) (int, bool) {
	if firstCheck || currentPeerCount != lastPeerCount {
		if currentPeerCount == 0 {
			n.logger.Warn("Node has no connected peers",
				zap.String("node_id", n.host.ID().String()))
		} else if currentPeerCount < lastPeerCount {
			n.logger.Info("Node lost peers",
				zap.Int("current_peers", currentPeerCount),
				zap.Int("previous_peers", lastPeerCount))
		} else if currentPeerCount > lastPeerCount && !firstCheck {
			n.logger.Debug("Node gained peers",
				zap.Int("current_peers", currentPeerCount),
				zap.Int("previous_peers", lastPeerCount))
		}

		lastPeerCount = currentPeerCount
		firstCheck = false
	}
	return lastPeerCount, firstCheck
}

func logDetailedPeerInfo(n *Node, currentPeerCount int, peers []peer.ID) {
	if time.Now().Unix()%300 == 0 && currentPeerCount > 0 {
		peerIDs := make([]string, 0, currentPeerCount)
		for _, p := range peers {
			peerIDs = append(peerIDs, p.String())
		}
		n.logger.Debug("Node peer status",
			zap.Int("peer_count", currentPeerCount),
			zap.Strings("peer_ids", peerIDs))
	}
}

// GetCPUUsagePercent samples CPU utilisation across interval. It blocks for
// that long, so it takes a context: on shutdown the sample is abandoned rather
// than holding the monitoring loop open for the rest of the window.
func GetCPUUsagePercent(ctx context.Context, interval time.Duration) (uint64, error) {
	before, err := cpu.Get()
	if err != nil {
		return 0, err
	}

	timer := time.NewTimer(interval)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return 0, ctx.Err()
	case <-timer.C:
	}

	after, err := cpu.Get()
	if err != nil {
		return 0, err
	}
	idle := float64(after.Idle - before.Idle)
	total := float64(after.Total - before.Total)
	if total == 0 {
		return 0, errors.New("Failed to get CPU usage")
	}
	usagePercent := (1.0 - idle/total) * 100.0
	return uint64(usagePercent), nil
}

// cpuSampleWindow is how long a single CPU utilisation sample spans.
const cpuSampleWindow = 3 * time.Second

func logSystemUsage(ctx context.Context, n *Node) (*memory.Stats, uint64) {
	mem, _ := memory.Get()

	totalCpu, err := GetCPUUsagePercent(ctx, cpuSampleWindow)
	if err != nil {
		if ctx.Err() == nil {
			n.logger.Error("Failed to get CPU usage", zap.Error(err))
		}
		return mem, 0
	}

	n.logger.Debug("Node CPU usage",
		zap.Float64("cpu_usage", float64(totalCpu)),
		zap.Float64("memory_usage_percent", float64(mem.Used)/float64(mem.Total)*100))

	return mem, totalCpu
}

func announceMetrics(n *Node, peers []peer.ID, cpuUsage uint64, memUsage *memory.Stats) error {
	if n.pubsub == nil {
		return nil
	}

	peerIDs := make([]string, 0, len(peers))
	for _, p := range peers {
		peerIDs = append(peerIDs, p.String())
	}

	msg := struct {
		PeerID        string                 `json:"peer_id"`
		PeerCount     int                    `json:"peer_count"`
		PeerIDs       []string               `json:"peer_ids,omitempty"`
		CPU           uint64                 `json:"cpu_usage"`
		Memory        uint64                 `json:"memory_usage"`
		Timestamp     int64                  `json:"timestamp"`
		ClusterHealth map[string]interface{} `json:"cluster_health,omitempty"`
	}{
		PeerID:    n.host.ID().String(),
		PeerCount: len(peers),
		PeerIDs:   peerIDs,
		CPU:       cpuUsage,
		Memory:    memUsage.Used,
		Timestamp: time.Now().Unix(),
	}

	// Add cluster health metrics if available
	if cd := n.getClusterDiscovery(); cd != nil {
		metrics := cd.GetMetrics()
		msg.ClusterHealth = map[string]interface{}{
			"cluster_size":        metrics.ClusterSize,
			"active_nodes":        metrics.ActiveNodes,
			"inactive_nodes":      metrics.InactiveNodes,
			"discovery_status":    metrics.DiscoveryStatus,
			"current_leader":      metrics.CurrentLeader,
			"average_peer_health": metrics.AveragePeerHealth,
			"last_update":         metrics.LastUpdate.Format(time.RFC3339),
		}
	}

	data, err := json.Marshal(msg)
	if err != nil {
		return err
	}

	ctx := context.Background()
	if err := n.pubsub.Publish(ctx, "monitoring", data); err != nil {
		return err
	}

	return nil
}

// GetClusterHealth returns cluster health information
func (n *Node) GetClusterHealth() map[string]interface{} {
	cd := n.getClusterDiscovery()
	if cd == nil {
		return map[string]interface{}{
			"status": "not_initialized",
		}
	}

	metrics := cd.GetMetrics()
	return map[string]interface{}{
		"cluster_size":        metrics.ClusterSize,
		"active_nodes":        metrics.ActiveNodes,
		"inactive_nodes":      metrics.InactiveNodes,
		"discovery_status":    metrics.DiscoveryStatus,
		"current_leader":      metrics.CurrentLeader,
		"average_peer_health": metrics.AveragePeerHealth,
		"last_update":         metrics.LastUpdate,
	}
}

// GetDiscoveryStatus returns discovery service status
func (n *Node) GetDiscoveryStatus() map[string]interface{} {
	cd := n.getClusterDiscovery()
	if cd == nil {
		return map[string]interface{}{
			"status":  "disabled",
			"message": "cluster discovery not initialized",
		}
	}

	metrics := cd.GetMetrics()
	status := "healthy"
	if metrics.DiscoveryStatus == "no_peers" {
		status = "warning"
	} else if metrics.DiscoveryStatus == "degraded" {
		status = "degraded"
	}

	return map[string]interface{}{
		"status":       status,
		"cluster_size": metrics.ClusterSize,
		"last_update":  metrics.LastUpdate,
	}
}

// monitoringInterval is how often the connection monitor samples peers and
// system usage.
const monitoringInterval = 30 * time.Second

// startConnectionMonitoring starts the peer and system-usage monitoring loop.
// It is safe to call more than once: only the first call starts the loop.
func (n *Node) startConnectionMonitoring(ctx context.Context) {
	n.monitoringOnce.Do(func() {
		go n.monitorConnections(ctx)
		n.logger.Debug("Lightweight connection monitoring started")
	})
}

func (n *Node) monitorConnections(ctx context.Context) {
	ticker := time.NewTicker(monitoringInterval)
	defer ticker.Stop()

	var lastPeerCount int
	firstCheck := true
	tickCount := 0

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}

		if n.host == nil {
			return
		}
		tickCount++

		// Get current peer count
		peers := n.host.Network().Peers()
		currentPeerCount := len(peers)

		// Only log if peer count changed or on first check
		lastPeerCount, firstCheck = logPeerStatus(n, currentPeerCount, lastPeerCount, firstCheck)

		// Log detailed peer info at debug level occasionally (every 5 minutes)
		logDetailedPeerInfo(n, currentPeerCount, peers)

		// Log system usage
		mem, cpuUsage := logSystemUsage(ctx, n)

		// Announce metrics
		if err := announceMetrics(n, peers, cpuUsage, mem); err != nil {
			n.logger.Error("Failed to announce metrics", zap.Error(err))
		}

		// Periodically update IPFS Cluster peer addresses
		// This discovers all cluster peers and updates peer_addresses in service.json
		// so IPFS Cluster can automatically connect to all discovered peers
		if cm := n.getClusterConfigManager(); cm != nil {
			// Discover from LibP2P connections every 2 ticks (once per minute)
			// Works even if cluster peers aren't connected yet
			if tickCount%2 == 0 {
				if err := n.discoverClusterPeers(cm); err != nil {
					n.logger.ComponentWarn(logging.ComponentNode, "Failed to discover cluster peers from LibP2P", zap.Error(err))
				} else {
					n.logger.ComponentInfo(logging.ComponentNode, "Cluster peer addresses discovered from LibP2P")
				}
			}

			// Update from cluster API every 4 ticks (once per 2 minutes)
			// Works once peers are already connected
			if tickCount%4 == 0 {
				if err := n.updateClusterPeers(cm); err != nil {
					n.logger.ComponentWarn(logging.ComponentNode, "Failed to update cluster peers during monitoring", zap.Error(err))
				} else {
					n.logger.ComponentInfo(logging.ComponentNode, "Cluster peer addresses updated during monitoring")
				}

				// Try to repair peer configuration
				if err := n.repairClusterPeers(cm); err != nil {
					n.logger.ComponentWarn(logging.ComponentNode, "Failed to repair peer addresses during monitoring", zap.Error(err))
				} else {
					n.logger.ComponentInfo(logging.ComponentNode, "Peer configuration repaired during monitoring")
				}
			}
		}
	}
}
