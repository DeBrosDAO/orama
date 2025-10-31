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

func GetCPUUsagePercent(n *Node, interval time.Duration) (uint64, error) {
	before, err := cpu.Get()
	if err != nil {
		return 0, err
	}
	time.Sleep(interval)
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

func logSystemUsage(n *Node) (*memory.Stats, uint64) {
	mem, _ := memory.Get()

	totalCpu, err := GetCPUUsagePercent(n, 3*time.Second)
	if err != nil {
		n.logger.Error("Failed to get CPU usage", zap.Error(err))
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
		PeerID         string                 `json:"peer_id"`
		PeerCount      int                    `json:"peer_count"`
		PeerIDs        []string               `json:"peer_ids,omitempty"`
		CPU            uint64                 `json:"cpu_usage"`
		Memory         uint64                 `json:"memory_usage"`
		Timestamp      int64                  `json:"timestamp"`
		ClusterHealth  map[string]interface{} `json:"cluster_health,omitempty"`
	}{
		PeerID:    n.host.ID().String(),
		PeerCount: len(peers),
		PeerIDs:   peerIDs,
		CPU:       cpuUsage,
		Memory:    memUsage.Used,
		Timestamp: time.Now().Unix(),
	}

	// Add cluster health metrics if available
	if n.clusterDiscovery != nil {
		metrics := n.clusterDiscovery.GetMetrics()
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
	if n.clusterDiscovery == nil {
		return map[string]interface{}{
			"status": "not_initialized",
		}
	}

	metrics := n.clusterDiscovery.GetMetrics()
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
	if n.clusterDiscovery == nil {
		return map[string]interface{}{
			"status":  "disabled",
			"message": "cluster discovery not initialized",
		}
	}

	metrics := n.clusterDiscovery.GetMetrics()
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

// startConnectionMonitoring starts minimal connection monitoring for the lightweight client.
// Unlike nodes which need extensive monitoring, clients only need basic health checks.
func (n *Node) startConnectionMonitoring() {
	go func() {
		ticker := time.NewTicker(30 * time.Second) // Less frequent than nodes (60s vs 30s)
		defer ticker.Stop()

		var lastPeerCount int
		firstCheck := true

		for range ticker.C {
			if n.host == nil {
				return
			}

			// Get current peer count
			peers := n.host.Network().Peers()
			currentPeerCount := len(peers)

			// Only log if peer count changed or on first check
			lastPeerCount, firstCheck = logPeerStatus(n, currentPeerCount, lastPeerCount, firstCheck)

			// Log detailed peer info at debug level occasionally (every 5 minutes)
			logDetailedPeerInfo(n, currentPeerCount, peers)

			// Log system usage
			mem, cpuUsage := logSystemUsage(n)

			// Announce metrics
			if err := announceMetrics(n, peers, cpuUsage, mem); err != nil {
				n.logger.Error("Failed to announce metrics", zap.Error(err))
			}
		}
	}()

	n.logger.Debug("Lightweight connection monitoring started")
}
