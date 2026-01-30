package node

import (
	"context"
	"fmt"
	"net"
	"os/exec"
	"strings"
	"time"

	"github.com/DeBrosOfficial/network/pkg/environments/production"
	"github.com/DeBrosOfficial/network/pkg/logging"
	"go.uber.org/zap"
)

// syncWireGuardPeers reads all peers from RQLite and reconciles the local
// WireGuard interface so it matches the cluster state. This is called on
// startup after RQLite is ready and periodically thereafter.
func (n *Node) syncWireGuardPeers(ctx context.Context) error {
	if n.rqliteAdapter == nil {
		return fmt.Errorf("rqlite adapter not initialized")
	}

	// Check if WireGuard is installed and active
	if _, err := exec.LookPath("wg"); err != nil {
		n.logger.ComponentInfo(logging.ComponentNode, "WireGuard not installed, skipping peer sync")
		return nil
	}

	// Check if wg0 interface exists
	out, err := exec.CommandContext(ctx, "sudo", "wg", "show", "wg0").CombinedOutput()
	if err != nil {
		n.logger.ComponentInfo(logging.ComponentNode, "WireGuard interface wg0 not active, skipping peer sync")
		return nil
	}

	// Parse current peers from wg show output
	currentPeers := parseWGShowPeers(string(out))
	localPubKey := parseWGShowLocalKey(string(out))

	// Query all peers from RQLite
	db := n.rqliteAdapter.GetSQLDB()
	rows, err := db.QueryContext(ctx,
		"SELECT node_id, wg_ip, public_key, public_ip, wg_port FROM wireguard_peers ORDER BY wg_ip")
	if err != nil {
		return fmt.Errorf("failed to query wireguard_peers: %w", err)
	}
	defer rows.Close()

	// Build desired peer set (excluding self)
	desiredPeers := make(map[string]production.WireGuardPeer)
	for rows.Next() {
		var nodeID, wgIP, pubKey, pubIP string
		var wgPort int
		if err := rows.Scan(&nodeID, &wgIP, &pubKey, &pubIP, &wgPort); err != nil {
			continue
		}
		if pubKey == localPubKey {
			continue // skip self
		}
		if wgPort == 0 {
			wgPort = 51820
		}
		desiredPeers[pubKey] = production.WireGuardPeer{
			PublicKey: pubKey,
			Endpoint:  fmt.Sprintf("%s:%d", pubIP, wgPort),
			AllowedIP: wgIP + "/32",
		}
	}

	wp := &production.WireGuardProvisioner{}

	// Add missing peers
	for pubKey, peer := range desiredPeers {
		if _, exists := currentPeers[pubKey]; !exists {
			if err := wp.AddPeer(peer); err != nil {
				n.logger.ComponentWarn(logging.ComponentNode, "failed to add WG peer",
					zap.String("public_key", pubKey[:8]+"..."),
					zap.Error(err))
			} else {
				n.logger.ComponentInfo(logging.ComponentNode, "added WG peer",
					zap.String("allowed_ip", peer.AllowedIP))
			}
		}
	}

	// Remove peers not in the desired set
	for pubKey := range currentPeers {
		if _, exists := desiredPeers[pubKey]; !exists {
			if err := wp.RemovePeer(pubKey); err != nil {
				n.logger.ComponentWarn(logging.ComponentNode, "failed to remove stale WG peer",
					zap.String("public_key", pubKey[:8]+"..."),
					zap.Error(err))
			} else {
				n.logger.ComponentInfo(logging.ComponentNode, "removed stale WG peer",
					zap.String("public_key", pubKey[:8]+"..."))
			}
		}
	}

	n.logger.ComponentInfo(logging.ComponentNode, "WireGuard peer sync completed",
		zap.Int("desired_peers", len(desiredPeers)),
		zap.Int("current_peers", len(currentPeers)))

	return nil
}

// ensureWireGuardSelfRegistered ensures this node's WireGuard info is in the
// wireguard_peers table. Without this, joining nodes get an empty peer list
// from the /v1/internal/join endpoint and can't establish WG tunnels.
func (n *Node) ensureWireGuardSelfRegistered(ctx context.Context) {
	if n.rqliteAdapter == nil {
		return
	}

	// Check if wg0 is active
	out, err := exec.CommandContext(ctx, "sudo", "wg", "show", "wg0").CombinedOutput()
	if err != nil {
		return // WG not active, nothing to register
	}

	// Get local public key
	localPubKey := parseWGShowLocalKey(string(out))
	if localPubKey == "" {
		return
	}

	// Get WG IP from interface
	wgIP := ""
	iface, err := net.InterfaceByName("wg0")
	if err != nil {
		return
	}
	addrs, err := iface.Addrs()
	if err != nil {
		return
	}
	for _, addr := range addrs {
		if ipnet, ok := addr.(*net.IPNet); ok && ipnet.IP.To4() != nil {
			wgIP = ipnet.IP.String()
			break
		}
	}
	if wgIP == "" {
		return
	}

	// Get public IP
	publicIP, err := n.getNodeIPAddress()
	if err != nil {
		return
	}

	nodeID := n.GetPeerID()
	if nodeID == "" {
		nodeID = fmt.Sprintf("node-%s", wgIP)
	}

	db := n.rqliteAdapter.GetSQLDB()
	_, err = db.ExecContext(ctx,
		"INSERT OR REPLACE INTO wireguard_peers (node_id, wg_ip, public_key, public_ip, wg_port) VALUES (?, ?, ?, ?, ?)",
		nodeID, wgIP, localPubKey, publicIP, 51820)
	if err != nil {
		n.logger.ComponentWarn(logging.ComponentNode, "Failed to self-register WG peer", zap.Error(err))
	} else {
		n.logger.ComponentInfo(logging.ComponentNode, "WireGuard self-registered",
			zap.String("wg_ip", wgIP),
			zap.String("public_key", localPubKey[:8]+"..."))
	}
}

// startWireGuardSyncLoop runs syncWireGuardPeers periodically
func (n *Node) startWireGuardSyncLoop(ctx context.Context) {
	// Ensure this node is registered in wireguard_peers (critical for join flow)
	n.ensureWireGuardSelfRegistered(ctx)

	// Run initial sync
	if err := n.syncWireGuardPeers(ctx); err != nil {
		n.logger.ComponentWarn(logging.ComponentNode, "initial WireGuard peer sync failed", zap.Error(err))
	}

	// Periodic sync every 60 seconds
	go func() {
		ticker := time.NewTicker(60 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := n.syncWireGuardPeers(ctx); err != nil {
					n.logger.ComponentWarn(logging.ComponentNode, "WireGuard peer sync failed", zap.Error(err))
				}
			}
		}
	}()
}

// parseWGShowPeers extracts public keys of current peers from `wg show wg0` output
func parseWGShowPeers(output string) map[string]struct{} {
	peers := make(map[string]struct{})
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "peer:") {
			key := strings.TrimSpace(strings.TrimPrefix(line, "peer:"))
			if key != "" {
				peers[key] = struct{}{}
			}
		}
	}
	return peers
}

// parseWGShowLocalKey extracts the local public key from `wg show wg0` output
func parseWGShowLocalKey(output string) string {
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "public key:") {
			return strings.TrimSpace(strings.TrimPrefix(line, "public key:"))
		}
	}
	return ""
}
