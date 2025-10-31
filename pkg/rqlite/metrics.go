package rqlite

import (
	"time"
)

// GetMetrics returns current cluster metrics
func (c *ClusterDiscoveryService) GetMetrics() *ClusterMetrics {
	c.mu.RLock()
	defer c.mu.RUnlock()
	
	activeCount := 0
	inactiveCount := 0
	totalHealth := 0.0
	currentLeader := ""
	
	now := time.Now()
	
	for nodeID, health := range c.peerHealth {
		if health.Status == "active" {
			activeCount++
			
			// Calculate health score (0-100) based on last seen
			timeSinceLastSeen := now.Sub(health.LastSeen)
			healthScore := 100.0
			if timeSinceLastSeen > time.Minute {
				// Degrade health score based on time since last seen
				healthScore = 100.0 - (float64(timeSinceLastSeen.Seconds()) / float64(c.inactivityLimit.Seconds()) * 100.0)
				if healthScore < 0 {
					healthScore = 0
				}
			}
			totalHealth += healthScore
		} else {
			inactiveCount++
		}
		
		// Try to determine leader
		if peer, ok := c.knownPeers[nodeID]; ok {
			// We'd need to check the actual leader status from RQLite
			// For now, bootstrap nodes are more likely to be leader
			if peer.NodeType == "bootstrap" && currentLeader == "" {
				currentLeader = nodeID
			}
		}
	}
	
	averageHealth := 0.0
	if activeCount > 0 {
		averageHealth = totalHealth / float64(activeCount)
	}
	
	// Determine discovery status
	discoveryStatus := "healthy"
	if len(c.knownPeers) == 0 {
		discoveryStatus = "no_peers"
	} else if len(c.knownPeers) == 1 {
		discoveryStatus = "single_node"
	} else if averageHealth < 50 {
		discoveryStatus = "degraded"
	}
	
	return &ClusterMetrics{
		ClusterSize:       len(c.knownPeers),
		ActiveNodes:       activeCount,
		InactiveNodes:     inactiveCount,
		RemovedNodes:      0, // Could track this with a counter
		LastUpdate:        c.lastUpdate,
		DiscoveryStatus:   discoveryStatus,
		CurrentLeader:     currentLeader,
		AveragePeerHealth: averageHealth,
	}
}

