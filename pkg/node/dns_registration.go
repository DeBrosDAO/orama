package node

import (
	"context"
	"fmt"
	"net"
	"time"

	"github.com/DeBrosOfficial/network/pkg/logging"
	"go.uber.org/zap"
)

// registerDNSNode registers this node in the dns_nodes table for deployment routing
func (n *Node) registerDNSNode(ctx context.Context) error {
	if n.rqliteAdapter == nil {
		return fmt.Errorf("rqlite adapter not initialized")
	}

	// Get node ID (use peer ID)
	nodeID := n.GetPeerID()
	if nodeID == "" {
		return fmt.Errorf("node peer ID not available")
	}

	// Get external IP address
	ipAddress, err := n.getNodeIPAddress()
	if err != nil {
		n.logger.ComponentWarn(logging.ComponentNode, "Failed to determine node IP, using localhost", zap.Error(err))
		ipAddress = "127.0.0.1"
	}

	// Get internal IP (same as external for now, or could use private network IP)
	internalIP := ipAddress

	// Determine region (defaulting to "local" for now, could be from cloud metadata in future)
	region := "local"

	// Insert or update node record
	query := `
		INSERT INTO dns_nodes (id, ip_address, internal_ip, region, status, last_seen, created_at, updated_at)
		VALUES (?, ?, ?, ?, 'active', datetime('now'), datetime('now'), datetime('now'))
		ON CONFLICT(id) DO UPDATE SET
			ip_address = excluded.ip_address,
			internal_ip = excluded.internal_ip,
			region = excluded.region,
			status = 'active',
			last_seen = datetime('now'),
			updated_at = datetime('now')
	`

	db := n.rqliteAdapter.GetSQLDB()
	_, err = db.ExecContext(ctx, query, nodeID, ipAddress, internalIP, region)
	if err != nil {
		return fmt.Errorf("failed to register DNS node: %w", err)
	}

	n.logger.ComponentInfo(logging.ComponentNode, "Registered DNS node",
		zap.String("node_id", nodeID),
		zap.String("ip_address", ipAddress),
		zap.String("region", region),
	)

	return nil
}

// startDNSHeartbeat starts a goroutine that periodically updates the node's last_seen timestamp
func (n *Node) startDNSHeartbeat(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				n.logger.ComponentInfo(logging.ComponentNode, "DNS heartbeat stopped")
				return
			case <-ticker.C:
				if err := n.updateDNSHeartbeat(ctx); err != nil {
					n.logger.ComponentWarn(logging.ComponentNode, "Failed to update DNS heartbeat", zap.Error(err))
				}
			}
		}
	}()

	n.logger.ComponentInfo(logging.ComponentNode, "Started DNS heartbeat (30s interval)")
}

// updateDNSHeartbeat updates the node's last_seen timestamp in dns_nodes
func (n *Node) updateDNSHeartbeat(ctx context.Context) error {
	if n.rqliteAdapter == nil {
		return fmt.Errorf("rqlite adapter not initialized")
	}

	nodeID := n.GetPeerID()
	if nodeID == "" {
		return fmt.Errorf("node peer ID not available")
	}

	query := `UPDATE dns_nodes SET last_seen = datetime('now'), updated_at = datetime('now') WHERE id = ?`
	db := n.rqliteAdapter.GetSQLDB()
	_, err := db.ExecContext(ctx, query, nodeID)
	if err != nil {
		return fmt.Errorf("failed to update DNS heartbeat: %w", err)
	}

	return nil
}

// getNodeIPAddress attempts to determine the node's external IP address
func (n *Node) getNodeIPAddress() (string, error) {
	// Try to detect external IP by connecting to a public server
	conn, err := net.Dial("udp", "8.8.8.8:80")
	if err != nil {
		// If that fails, try to get first non-loopback interface IP
		addrs, err := net.InterfaceAddrs()
		if err != nil {
			return "", err
		}

		for _, addr := range addrs {
			if ipnet, ok := addr.(*net.IPNet); ok && !ipnet.IP.IsLoopback() {
				if ipnet.IP.To4() != nil {
					return ipnet.IP.String(), nil
				}
			}
		}

		return "", fmt.Errorf("no suitable IP address found")
	}
	defer conn.Close()

	localAddr := conn.LocalAddr().(*net.UDPAddr)
	return localAddr.IP.String(), nil
}
