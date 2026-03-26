package node

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os/exec"
	"strings"
	"time"

	"github.com/DeBrosOfficial/network/pkg/logging"
	"go.uber.org/zap"
)

// syncIPFSSwarmPeers queries all cluster nodes from RQLite and ensures
// this node's IPFS daemon is connected to every other node's IPFS daemon.
// Uses `ipfs swarm connect` for immediate connectivity without requiring
// config file changes or IPFS restarts.
func (n *Node) syncIPFSSwarmPeers(ctx context.Context) {
	if n.rqliteAdapter == nil {
		return
	}

	// Check if IPFS is running
	if _, err := exec.LookPath("ipfs"); err != nil {
		return
	}

	// Get this node's WG IP
	myWGIP := getLocalWGIP()
	if myWGIP == "" {
		return
	}

	// Query all peers with IPFS peer IDs from RQLite
	db := n.rqliteAdapter.GetSQLDB()
	rows, err := db.QueryContext(ctx,
		"SELECT wg_ip, ipfs_peer_id FROM wireguard_peers WHERE ipfs_peer_id != '' AND wg_ip != ?",
		myWGIP)
	if err != nil {
		n.logger.ComponentWarn(logging.ComponentNode, "Failed to query IPFS peers from RQLite", zap.Error(err))
		return
	}
	defer rows.Close()

	type ipfsPeer struct {
		wgIP   string
		peerID string
	}

	var peers []ipfsPeer
	for rows.Next() {
		var p ipfsPeer
		if err := rows.Scan(&p.wgIP, &p.peerID); err != nil {
			continue
		}
		peers = append(peers, p)
	}

	if len(peers) == 0 {
		return
	}

	// Get currently connected IPFS swarm peers via API
	connectedPeers := getConnectedIPFSPeers()

	// Connect to any peer we're not already connected to
	connected := 0
	for _, p := range peers {
		if connectedPeers[p.peerID] {
			continue // already connected
		}

		multiaddr := fmt.Sprintf("/ip4/%s/tcp/4101/p2p/%s", p.wgIP, p.peerID)
		if err := ipfsSwarmConnect(multiaddr); err != nil {
			n.logger.ComponentWarn(logging.ComponentNode, "Failed to connect IPFS swarm peer",
				zap.String("peer", p.peerID[:12]+"..."),
				zap.String("wg_ip", p.wgIP),
				zap.Error(err))
		} else {
			connected++
			n.logger.ComponentInfo(logging.ComponentNode, "Connected to IPFS swarm peer",
				zap.String("peer", p.peerID[:12]+"..."),
				zap.String("wg_ip", p.wgIP))
		}
	}

	if connected > 0 {
		n.logger.ComponentInfo(logging.ComponentNode, "IPFS swarm sync completed",
			zap.Int("new_connections", connected),
			zap.Int("total_cluster_peers", len(peers)))
	}
}

// getConnectedIPFSPeers returns a set of currently connected IPFS peer IDs
func getConnectedIPFSPeers() map[string]bool {
	peers := make(map[string]bool)

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Post("http://localhost:4501/api/v0/swarm/peers", "", nil)
	if err != nil {
		return peers
	}
	defer resp.Body.Close()

	// The response contains Peers array with Peer field for each connected peer
	// We just need the peer IDs, which are the last component of each multiaddr
	var result struct {
		Peers []struct {
			Peer string `json:"Peer"`
		} `json:"Peers"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return peers
	}

	for _, p := range result.Peers {
		peers[p.Peer] = true
	}
	return peers
}

// ipfsSwarmConnect connects to an IPFS peer via the HTTP API
func ipfsSwarmConnect(multiaddr string) error {
	client := &http.Client{Timeout: 10 * time.Second}
	apiURL := fmt.Sprintf("http://localhost:4501/api/v0/swarm/connect?arg=%s", url.QueryEscape(multiaddr))
	resp, err := client.Post(apiURL, "", nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("swarm connect returned status %d", resp.StatusCode)
	}
	return nil
}

// getLocalWGIP returns the WireGuard IP of this node
func getLocalWGIP() string {
	out, err := exec.Command("ip", "-4", "addr", "show", "wg0").CombinedOutput()
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "inet ") {
			parts := strings.Fields(line)
			if len(parts) >= 2 {
				return strings.Split(parts[1], "/")[0]
			}
		}
	}
	return ""
}

// startIPFSSwarmSyncLoop periodically syncs IPFS swarm connections with cluster peers
func (n *Node) startIPFSSwarmSyncLoop(ctx context.Context) {
	// Initial sync after a short delay (give IPFS time to start)
	go func() {
		select {
		case <-ctx.Done():
			return
		case <-time.After(30 * time.Second):
		}

		n.syncIPFSSwarmPeers(ctx)

		// Then sync every 60 seconds
		ticker := time.NewTicker(60 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				n.syncIPFSSwarmPeers(ctx)
			}
		}
	}()

	n.logger.ComponentInfo(logging.ComponentNode, "IPFS swarm sync loop started")
}
