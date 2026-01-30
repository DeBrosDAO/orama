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

	// Get internal IP from WireGuard interface (for cross-node communication over VPN)
	internalIP := ipAddress
	if wgIP, err := n.getWireGuardIP(); err == nil && wgIP != "" {
		internalIP = wgIP
	}

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
				// Self-healing: ensure this node's DNS records exist on every heartbeat
				if err := n.ensureBaseDNSRecords(ctx); err != nil {
					n.logger.ComponentWarn(logging.ComponentNode, "Failed to ensure DNS records on heartbeat", zap.Error(err))
				}
				// Remove DNS records for nodes that stopped heartbeating
				n.cleanupStaleNodeRecords(ctx)
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

// ensureBaseDNSRecords ensures this node's IP is present in the base DNS records.
// This provides self-healing: if records are missing (fresh install, DB reset),
// the node recreates them on startup. Each node only manages its own IP entries.
func (n *Node) ensureBaseDNSRecords(ctx context.Context) error {
	domain := n.config.Node.Domain
	if domain == "" {
		domain = n.config.HTTPGateway.BaseDomain
	}
	if domain == "" {
		return nil // No domain configured, skip
	}

	ipAddress, err := n.getNodeIPAddress()
	if err != nil {
		return fmt.Errorf("failed to determine node IP: %w", err)
	}

	// Ensure trailing dot for FQDN format (as CoreDNS expects)
	fqdn := domain + "."
	wildcardFQDN := "*." + domain + "."

	db := n.rqliteAdapter.GetSQLDB()

	// Insert root A record and wildcard A record for this node's IP
	// ON CONFLICT DO NOTHING avoids duplicates (UNIQUE on fqdn, record_type, value)
	records := []struct {
		fqdn  string
		value string
	}{
		{fqdn, ipAddress},
		{wildcardFQDN, ipAddress},
	}

	for _, r := range records {
		query := `INSERT INTO dns_records (fqdn, record_type, value, ttl, namespace, created_by, is_active, created_at, updated_at)
			VALUES (?, 'A', ?, 300, 'system', 'system', TRUE, datetime('now'), datetime('now'))
			ON CONFLICT(fqdn, record_type, value) DO NOTHING`
		if _, err := db.ExecContext(ctx, query, r.fqdn, r.value); err != nil {
			n.logger.ComponentWarn(logging.ComponentNode, "Failed to ensure DNS record",
				zap.String("fqdn", r.fqdn), zap.Error(err))
		}
	}

	// Claim an NS slot if available (ns1, ns2, or ns3)
	n.claimNameserverSlot(ctx, domain, ipAddress)

	return nil
}

// claimNameserverSlot attempts to claim an available NS hostname (ns1/ns2/ns3) for this node.
// If the node already has a slot, it updates the IP. If no slot is available, it does nothing.
func (n *Node) claimNameserverSlot(ctx context.Context, domain, ipAddress string) {
	nodeID := n.GetPeerID()
	db := n.rqliteAdapter.GetSQLDB()

	// Check if this node already has a slot
	var existingHostname string
	err := db.QueryRowContext(ctx,
		`SELECT hostname FROM dns_nameservers WHERE node_id = ? AND domain = ?`,
		nodeID, domain,
	).Scan(&existingHostname)

	if err == nil {
		// Already claimed — update IP if changed
		if _, err := db.ExecContext(ctx,
			`UPDATE dns_nameservers SET ip_address = ?, updated_at = datetime('now') WHERE hostname = ? AND domain = ?`,
			ipAddress, existingHostname, domain,
		); err != nil {
			n.logger.ComponentWarn(logging.ComponentNode, "Failed to update NS slot IP", zap.Error(err))
		}
		// Ensure the glue A record matches
		nsFQDN := existingHostname + "." + domain + "."
		if _, err := db.ExecContext(ctx,
			`INSERT INTO dns_records (fqdn, record_type, value, ttl, namespace, created_by, is_active, created_at, updated_at)
			VALUES (?, 'A', ?, 300, 'system', 'system', TRUE, datetime('now'), datetime('now'))
			ON CONFLICT(fqdn, record_type, value) DO NOTHING`,
			nsFQDN, ipAddress,
		); err != nil {
			n.logger.ComponentWarn(logging.ComponentNode, "Failed to ensure NS glue record", zap.Error(err))
		}
		return
	}

	// Try to claim an available slot
	for _, hostname := range []string{"ns1", "ns2", "ns3"} {
		result, err := db.ExecContext(ctx,
			`INSERT INTO dns_nameservers (hostname, node_id, ip_address, domain) VALUES (?, ?, ?, ?)
			ON CONFLICT(hostname) DO NOTHING`,
			hostname, nodeID, ipAddress, domain,
		)
		if err != nil {
			continue
		}
		rows, _ := result.RowsAffected()
		if rows > 0 {
			// Successfully claimed this slot — create glue record
			nsFQDN := hostname + "." + domain + "."
			if _, err := db.ExecContext(ctx,
				`INSERT INTO dns_records (fqdn, record_type, value, ttl, namespace, created_by, is_active, created_at, updated_at)
				VALUES (?, 'A', ?, 300, 'system', 'system', TRUE, datetime('now'), datetime('now'))
				ON CONFLICT(fqdn, record_type, value) DO NOTHING`,
				nsFQDN, ipAddress,
			); err != nil {
				n.logger.ComponentWarn(logging.ComponentNode, "Failed to create NS glue record", zap.Error(err))
			}
			n.logger.ComponentInfo(logging.ComponentNode, "Claimed NS slot",
				zap.String("hostname", hostname),
				zap.String("ip", ipAddress),
			)
			return
		}
	}
}

// cleanupStaleNodeRecords removes A records for nodes that have stopped heartbeating.
// This ensures DNS only returns IPs for healthy, active nodes.
func (n *Node) cleanupStaleNodeRecords(ctx context.Context) {
	if n.rqliteAdapter == nil {
		return
	}

	domain := n.config.Node.Domain
	if domain == "" {
		domain = n.config.HTTPGateway.BaseDomain
	}
	if domain == "" {
		return
	}

	db := n.rqliteAdapter.GetSQLDB()

	// Find nodes that haven't sent a heartbeat in over 2 minutes
	staleQuery := `SELECT id, ip_address FROM dns_nodes WHERE status = 'active' AND last_seen < datetime('now', '-120 seconds')`
	rows, err := db.QueryContext(ctx, staleQuery)
	if err != nil {
		n.logger.ComponentWarn(logging.ComponentNode, "Failed to query stale nodes", zap.Error(err))
		return
	}
	defer rows.Close()

	fqdn := domain + "."
	wildcardFQDN := "*." + domain + "."

	for rows.Next() {
		var nodeID, ip string
		if err := rows.Scan(&nodeID, &ip); err != nil {
			continue
		}

		// Mark node as inactive
		if _, err := db.ExecContext(ctx, `UPDATE dns_nodes SET status = 'inactive', updated_at = datetime('now') WHERE id = ?`, nodeID); err != nil {
			n.logger.ComponentWarn(logging.ComponentNode, "Failed to mark node inactive", zap.String("node_id", nodeID), zap.Error(err))
		}

		// Remove the dead node's A records from round-robin
		for _, f := range []string{fqdn, wildcardFQDN} {
			if _, err := db.ExecContext(ctx, `DELETE FROM dns_records WHERE fqdn = ? AND record_type = 'A' AND value = ? AND namespace = 'system'`, f, ip); err != nil {
				n.logger.ComponentWarn(logging.ComponentNode, "Failed to remove stale DNS record",
					zap.String("fqdn", f), zap.String("ip", ip), zap.Error(err))
			}
		}

		// Release any NS slot held by this dead node
		if _, err := db.ExecContext(ctx, `DELETE FROM dns_nameservers WHERE node_id = ?`, nodeID); err != nil {
			n.logger.ComponentWarn(logging.ComponentNode, "Failed to release NS slot", zap.String("node_id", nodeID), zap.Error(err))
		}

		// Remove glue records for this node's IP (ns1.domain., ns2.domain., ns3.domain.)
		for _, ns := range []string{"ns1", "ns2", "ns3"} {
			nsFQDN := ns + "." + domain + "."
			if _, err := db.ExecContext(ctx,
				`DELETE FROM dns_records WHERE fqdn = ? AND record_type = 'A' AND value = ? AND namespace = 'system'`,
				nsFQDN, ip,
			); err != nil {
				n.logger.ComponentWarn(logging.ComponentNode, "Failed to remove NS glue record", zap.Error(err))
			}
		}

		n.logger.ComponentInfo(logging.ComponentNode, "Removed stale node from DNS",
			zap.String("node_id", nodeID),
			zap.String("ip", ip),
		)
	}
}

// getWireGuardIP returns the IPv4 address assigned to the wg0 interface, if any
func (n *Node) getWireGuardIP() (string, error) {
	iface, err := net.InterfaceByName("wg0")
	if err != nil {
		return "", err
	}
	addrs, err := iface.Addrs()
	if err != nil {
		return "", err
	}
	for _, addr := range addrs {
		if ipnet, ok := addr.(*net.IPNet); ok && ipnet.IP.To4() != nil {
			return ipnet.IP.String(), nil
		}
	}
	return "", fmt.Errorf("no IPv4 address on wg0")
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
