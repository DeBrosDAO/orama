package gateway

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/multiformats/go-multiaddr"
	"go.uber.org/zap"
)

// PeerDiscovery manages namespace gateway peer discovery via RQLite
type PeerDiscovery struct {
	host       host.Host
	rqliteDB   *sql.DB
	nodeID     string
	listenPort int
	namespace  string
	logger     *zap.Logger

	// Stop channel for background goroutines
	stopCh chan struct{}
}

// NewPeerDiscovery creates a new peer discovery manager
func NewPeerDiscovery(h host.Host, rqliteDB *sql.DB, nodeID string, listenPort int, namespace string, logger *zap.Logger) *PeerDiscovery {
	return &PeerDiscovery{
		host:       h,
		rqliteDB:   rqliteDB,
		nodeID:     nodeID,
		listenPort: listenPort,
		namespace:  namespace,
		logger:     logger,
		stopCh:     make(chan struct{}),
	}
}

// Start initializes the peer discovery system
func (pd *PeerDiscovery) Start(ctx context.Context) error {
	pd.logger.Info("Starting peer discovery",
		zap.String("namespace", pd.namespace),
		zap.String("peer_id", pd.host.ID().String()),
		zap.String("node_id", pd.nodeID))

	// 1. Create discovery table if it doesn't exist
	if err := pd.initTable(ctx); err != nil {
		return fmt.Errorf("failed to initialize discovery table: %w", err)
	}

	// 2. Register ourselves
	if err := pd.registerSelf(ctx); err != nil {
		return fmt.Errorf("failed to register self: %w", err)
	}

	// 3. Discover and connect to existing peers
	if err := pd.discoverPeers(ctx); err != nil {
		pd.logger.Warn("Initial peer discovery failed (will retry in background)",
			zap.Error(err))
	}

	// 4. Start background goroutines
	go pd.heartbeatLoop(ctx)
	go pd.discoveryLoop(ctx)

	pd.logger.Info("Peer discovery started successfully",
		zap.String("namespace", pd.namespace))

	return nil
}

// Stop stops the peer discovery system
func (pd *PeerDiscovery) Stop(ctx context.Context) error {
	pd.logger.Info("Stopping peer discovery",
		zap.String("namespace", pd.namespace))

	// Signal background goroutines to stop
	close(pd.stopCh)

	// Unregister ourselves from the discovery table
	if err := pd.unregisterSelf(ctx); err != nil {
		pd.logger.Warn("Failed to unregister self from discovery table",
			zap.Error(err))
	}

	pd.logger.Info("Peer discovery stopped",
		zap.String("namespace", pd.namespace))

	return nil
}

// initTable creates the peer discovery table if it doesn't exist
func (pd *PeerDiscovery) initTable(ctx context.Context) error {
	query := `
		CREATE TABLE IF NOT EXISTS _namespace_libp2p_peers (
			peer_id TEXT PRIMARY KEY,
			multiaddr TEXT NOT NULL,
			node_id TEXT NOT NULL,
			listen_port INTEGER NOT NULL,
			namespace TEXT NOT NULL,
			last_seen TIMESTAMP NOT NULL
		)
	`

	_, err := pd.rqliteDB.ExecContext(ctx, query)
	if err != nil {
		return fmt.Errorf("failed to create discovery table: %w", err)
	}

	pd.logger.Debug("Peer discovery table initialized",
		zap.String("namespace", pd.namespace))

	return nil
}

// registerSelf registers this gateway in the discovery table
func (pd *PeerDiscovery) registerSelf(ctx context.Context) error {
	peerID := pd.host.ID().String()

	// Get WireGuard IP from host addresses
	wireguardIP, err := pd.getWireGuardIP()
	if err != nil {
		return fmt.Errorf("failed to get WireGuard IP: %w", err)
	}

	// Build multiaddr: /ip4/<wireguard_ip>/tcp/<port>/p2p/<peer_id>
	multiaddr := fmt.Sprintf("/ip4/%s/tcp/%d/p2p/%s", wireguardIP, pd.listenPort, peerID)

	query := `
		INSERT OR REPLACE INTO _namespace_libp2p_peers
		(peer_id, multiaddr, node_id, listen_port, namespace, last_seen)
		VALUES (?, ?, ?, ?, ?, ?)
	`

	_, err = pd.rqliteDB.ExecContext(ctx, query,
		peerID,
		multiaddr,
		pd.nodeID,
		pd.listenPort,
		pd.namespace,
		time.Now().UTC())

	if err != nil {
		return fmt.Errorf("failed to register self in discovery table: %w", err)
	}

	pd.logger.Info("Registered self in peer discovery",
		zap.String("peer_id", peerID),
		zap.String("multiaddr", multiaddr),
		zap.String("node_id", pd.nodeID))

	return nil
}

// unregisterSelf removes this gateway from the discovery table
func (pd *PeerDiscovery) unregisterSelf(ctx context.Context) error {
	peerID := pd.host.ID().String()

	query := `DELETE FROM _namespace_libp2p_peers WHERE peer_id = ?`

	_, err := pd.rqliteDB.ExecContext(ctx, query, peerID)
	if err != nil {
		return fmt.Errorf("failed to unregister self: %w", err)
	}

	pd.logger.Info("Unregistered self from peer discovery",
		zap.String("peer_id", peerID))

	return nil
}

// discoverPeers queries RQLite for other namespace gateways and connects to them
func (pd *PeerDiscovery) discoverPeers(ctx context.Context) error {
	myPeerID := pd.host.ID().String()

	// Query for peers that have been seen in the last 5 minutes
	query := `
		SELECT peer_id, multiaddr, node_id
		FROM _namespace_libp2p_peers
		WHERE peer_id != ?
		AND namespace = ?
		AND last_seen > datetime('now', '-5 minutes')
	`

	rows, err := pd.rqliteDB.QueryContext(ctx, query, myPeerID, pd.namespace)
	if err != nil {
		return fmt.Errorf("failed to query peers: %w", err)
	}
	defer rows.Close()

	discoveredCount := 0
	connectedCount := 0

	for rows.Next() {
		var peerID, multiaddrStr, nodeID string
		if err := rows.Scan(&peerID, &multiaddrStr, &nodeID); err != nil {
			pd.logger.Warn("Failed to scan peer row", zap.Error(err))
			continue
		}

		discoveredCount++

		// Parse peer ID
		remotePeerID, err := peer.Decode(peerID)
		if err != nil {
			pd.logger.Warn("Failed to decode peer ID",
				zap.String("peer_id", peerID),
				zap.Error(err))
			continue
		}

		// Parse multiaddr
		maddr, err := multiaddr.NewMultiaddr(multiaddrStr)
		if err != nil {
			pd.logger.Warn("Failed to parse multiaddr",
				zap.String("multiaddr", multiaddrStr),
				zap.Error(err))
			continue
		}

		// Check if already connected
		connectedness := pd.host.Network().Connectedness(remotePeerID)
		if connectedness == 1 { // Connected
			pd.logger.Debug("Already connected to peer",
				zap.String("peer_id", peerID),
				zap.String("node_id", nodeID))
			connectedCount++
			continue
		}

		// Convert multiaddr to peer.AddrInfo
		addrInfo, err := peer.AddrInfoFromP2pAddr(maddr)
		if err != nil {
			pd.logger.Warn("Failed to create AddrInfo",
				zap.String("multiaddr", multiaddrStr),
				zap.Error(err))
			continue
		}

		// Connect to peer
		connectCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		err = pd.host.Connect(connectCtx, *addrInfo)
		cancel()

		if err != nil {
			pd.logger.Warn("Failed to connect to peer",
				zap.String("peer_id", peerID),
				zap.String("node_id", nodeID),
				zap.String("multiaddr", multiaddrStr),
				zap.Error(err))
			continue
		}

		pd.logger.Info("Connected to namespace gateway peer",
			zap.String("peer_id", peerID),
			zap.String("node_id", nodeID),
			zap.String("multiaddr", multiaddrStr))

		connectedCount++
	}

	if err := rows.Err(); err != nil {
		return fmt.Errorf("error iterating peer rows: %w", err)
	}

	pd.logger.Info("Peer discovery completed",
		zap.Int("discovered", discoveredCount),
		zap.Int("connected", connectedCount))

	return nil
}

// heartbeatLoop periodically updates the last_seen timestamp
func (pd *PeerDiscovery) heartbeatLoop(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-pd.stopCh:
			return
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := pd.updateHeartbeat(ctx); err != nil {
				pd.logger.Warn("Failed to update heartbeat",
					zap.Error(err))
			}
		}
	}
}

// discoveryLoop periodically discovers new peers
func (pd *PeerDiscovery) discoveryLoop(ctx context.Context) {
	ticker := time.NewTicker(60 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-pd.stopCh:
			return
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := pd.discoverPeers(ctx); err != nil {
				pd.logger.Warn("Periodic peer discovery failed",
					zap.Error(err))
			}
		}
	}
}

// updateHeartbeat updates the last_seen timestamp for this gateway
func (pd *PeerDiscovery) updateHeartbeat(ctx context.Context) error {
	peerID := pd.host.ID().String()

	query := `
		UPDATE _namespace_libp2p_peers
		SET last_seen = ?
		WHERE peer_id = ?
	`

	_, err := pd.rqliteDB.ExecContext(ctx, query, time.Now().UTC(), peerID)
	if err != nil {
		return fmt.Errorf("failed to update heartbeat: %w", err)
	}

	pd.logger.Debug("Updated heartbeat",
		zap.String("peer_id", peerID))

	return nil
}

// getWireGuardIP extracts the WireGuard IP from the WireGuard interface
func (pd *PeerDiscovery) getWireGuardIP() (string, error) {
	// Method 1: Use 'ip addr show wg0' command (works without root)
	ip, err := pd.getWireGuardIPFromInterface()
	if err == nil {
		pd.logger.Info("Found WireGuard IP from network interface",
			zap.String("ip", ip))
		return ip, nil
	}
	pd.logger.Debug("Failed to get WireGuard IP from interface", zap.Error(err))

	// Method 2: Try to read from WireGuard config file (requires root, may fail)
	configPath := "/etc/wireguard/wg0.conf"
	data, err := os.ReadFile(configPath)
	if err == nil {
		// Parse Address line from config
		lines := strings.Split(string(data), "\n")
		for _, line := range lines {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "Address") {
				// Format: Address = 10.0.0.X/24
				parts := strings.Split(line, "=")
				if len(parts) == 2 {
					addrWithCIDR := strings.TrimSpace(parts[1])
					// Remove /24 suffix
					ip := strings.Split(addrWithCIDR, "/")[0]
					ip = strings.TrimSpace(ip)
					pd.logger.Info("Found WireGuard IP from config",
						zap.String("ip", ip))
					return ip, nil
				}
			}
		}
	}
	pd.logger.Debug("Failed to read WireGuard config", zap.Error(err))

	// Method 3: Fallback - Try to get from libp2p host addresses
	for _, addr := range pd.host.Addrs() {
		addrStr := addr.String()
		// Look for /ip4/10.0.0.x pattern
		if len(addrStr) > 10 && addrStr[:9] == "/ip4/10.0" {
			// Extract IP address
			parts := addr.String()
			// Parse /ip4/<ip>/... format
			if len(parts) > 5 {
				// Find the IP between /ip4/ and next /
				start := 5 // after "/ip4/"
				end := start
				for end < len(parts) && parts[end] != '/' {
					end++
				}
				if end > start {
					ip := parts[start:end]
					pd.logger.Info("Found WireGuard IP from libp2p addresses",
						zap.String("ip", ip))
					return ip, nil
				}
			}
		}
	}

	return "", fmt.Errorf("could not determine WireGuard IP")
}

// getWireGuardIPFromInterface gets the WireGuard IP using 'ip addr show wg0'
func (pd *PeerDiscovery) getWireGuardIPFromInterface() (string, error) {
	cmd := exec.Command("ip", "addr", "show", "wg0")
	output, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("failed to run 'ip addr show wg0': %w", err)
	}

	// Parse output to find inet line
	// Example: "    inet 10.0.0.4/24 scope global wg0"
	lines := strings.Split(string(output), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "inet ") && !strings.Contains(line, "inet6") {
			// Extract IP address (first field after "inet ")
			fields := strings.Fields(line)
			if len(fields) >= 2 {
				// Remove CIDR notation (/24)
				addrWithCIDR := fields[1]
				ip := strings.Split(addrWithCIDR, "/")[0]

				// Verify it's a 10.0.0.x address
				if strings.HasPrefix(ip, "10.0.0.") {
					return ip, nil
				}
			}
		}
	}

	return "", fmt.Errorf("could not find WireGuard IP in 'ip addr show wg0' output")
}
