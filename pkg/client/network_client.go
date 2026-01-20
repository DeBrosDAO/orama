package client

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/multiformats/go-multiaddr"
)

// NetworkInfoImpl implements NetworkInfo
type NetworkInfoImpl struct {
	client *Client
}

// GetPeers returns information about connected peers
func (n *NetworkInfoImpl) GetPeers(ctx context.Context) ([]PeerInfo, error) {
	if !n.client.isConnected() {
		return nil, fmt.Errorf("client not connected")
	}

	if err := n.client.requireAccess(ctx); err != nil {
		return nil, fmt.Errorf("authentication required: %w - run CLI commands to authenticate automatically", err)
	}

	// Get peers from LibP2P host
	host := n.client.host
	if host == nil {
		return nil, fmt.Errorf("no host available")
	}

	// Get connected peers
	connectedPeers := host.Network().Peers()
	peers := make([]PeerInfo, 0, len(connectedPeers)+1) // +1 for self

	// Add connected peers
	for _, peerID := range connectedPeers {
		// Get peer addresses
		peerInfo := host.Peerstore().PeerInfo(peerID)

		// Convert multiaddrs to strings
		addrs := make([]string, len(peerInfo.Addrs))
		for i, addr := range peerInfo.Addrs {
			addrs[i] = addr.String()
		}

		peers = append(peers, PeerInfo{
			ID:        peerID.String(),
			Addresses: addrs,
			Connected: true,
			LastSeen:  time.Now(), // LibP2P doesn't track last seen, so use current time
		})
	}

	// Add self node
	selfPeerInfo := host.Peerstore().PeerInfo(host.ID())
	selfAddrs := make([]string, len(selfPeerInfo.Addrs))
	for i, addr := range selfPeerInfo.Addrs {
		selfAddrs[i] = addr.String()
	}

	// Insert self node at the beginning of the list
	selfPeer := PeerInfo{
		ID:        host.ID().String(),
		Addresses: selfAddrs,
		Connected: true,
		LastSeen:  time.Now(),
	}

	// Prepend self to the list
	peers = append([]PeerInfo{selfPeer}, peers...)

	return peers, nil
}

// GetStatus returns network status
func (n *NetworkInfoImpl) GetStatus(ctx context.Context) (*NetworkStatus, error) {
	if !n.client.isConnected() {
		return nil, fmt.Errorf("client not connected")
	}

	if err := n.client.requireAccess(ctx); err != nil {
		return nil, fmt.Errorf("authentication required: %w - run CLI commands to authenticate automatically", err)
	}

	host := n.client.host
	if host == nil {
		return nil, fmt.Errorf("no host available")
	}

	// Get actual network status
	connectedPeers := host.Network().Peers()

	// Try to get database size from RQLite (optional - don't fail if unavailable)
	var dbSize int64 = 0
	dbClient := n.client.database
	if conn, err := dbClient.getRQLiteConnection(); err == nil {
		// Query database size (rough estimate)
		if result, err := conn.QueryOne("SELECT page_count * page_size as size FROM pragma_page_count(), pragma_page_size()"); err == nil {
			for result.Next() {
				if row, err := result.Slice(); err == nil && len(row) > 0 {
					if size, ok := row[0].(int64); ok {
						dbSize = size
					}
				}
			}
		}
	}

	// Try to get IPFS peer info (optional - don't fail if unavailable)
	ipfsInfo := queryIPFSPeerInfo()

	// Try to get IPFS Cluster peer info (optional - don't fail if unavailable)
	ipfsClusterInfo := queryIPFSClusterPeerInfo()

	return &NetworkStatus{
		NodeID:       host.ID().String(),
		PeerID:       host.ID().String(),
		Connected:    true,
		PeerCount:    len(connectedPeers),
		DatabaseSize: dbSize,
		Uptime:       time.Since(n.client.startTime),
		IPFS:         ipfsInfo,
		IPFSCluster:  ipfsClusterInfo,
	}, nil
}

// queryIPFSPeerInfo queries the local IPFS API for peer information
// Returns nil if IPFS is not running or unavailable
func queryIPFSPeerInfo() *IPFSPeerInfo {
	// IPFS API typically runs on port 4501 in our setup
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Post("http://localhost:4501/api/v0/id", "", nil)
	if err != nil {
		return nil // IPFS not available
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil
	}

	var result struct {
		ID        string   `json:"ID"`
		Addresses []string `json:"Addresses"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil
	}

	// Filter addresses to only include public/routable ones
	var swarmAddrs []string
	for _, addr := range result.Addresses {
		// Skip loopback and private addresses for external discovery
		if !strings.Contains(addr, "127.0.0.1") && !strings.Contains(addr, "/ip6/::1") {
			swarmAddrs = append(swarmAddrs, addr)
		}
	}

	return &IPFSPeerInfo{
		PeerID:         result.ID,
		SwarmAddresses: swarmAddrs,
	}
}

// queryIPFSClusterPeerInfo queries the local IPFS Cluster API for peer information
// Returns nil if IPFS Cluster is not running or unavailable
func queryIPFSClusterPeerInfo() *IPFSClusterPeerInfo {
	// IPFS Cluster API typically runs on port 9094 in our setup
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get("http://localhost:9094/id")
	if err != nil {
		return nil // IPFS Cluster not available
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil
	}

	var result struct {
		ID        string   `json:"id"`
		Addresses []string `json:"addresses"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil
	}

	// Filter addresses to only include public/routable ones for cluster discovery
	var clusterAddrs []string
	for _, addr := range result.Addresses {
		// Skip loopback addresses - only keep routable addresses
		if !strings.Contains(addr, "127.0.0.1") && !strings.Contains(addr, "/ip6/::1") {
			clusterAddrs = append(clusterAddrs, addr)
		}
	}

	return &IPFSClusterPeerInfo{
		PeerID:    result.ID,
		Addresses: clusterAddrs,
	}
}

// ConnectToPeer connects to a specific peer
func (n *NetworkInfoImpl) ConnectToPeer(ctx context.Context, peerAddr string) error {
	if !n.client.isConnected() {
		return fmt.Errorf("client not connected")
	}

	if err := n.client.requireAccess(ctx); err != nil {
		return fmt.Errorf("authentication required: %w - run CLI commands to authenticate automatically", err)
	}

	host := n.client.host
	if host == nil {
		return fmt.Errorf("no host available")
	}

	// Parse the multiaddr
	ma, err := multiaddr.NewMultiaddr(peerAddr)
	if err != nil {
		return fmt.Errorf("invalid multiaddr: %w", err)
	}

	// Extract peer info
	peerInfo, err := peer.AddrInfoFromP2pAddr(ma)
	if err != nil {
		return fmt.Errorf("failed to extract peer info: %w", err)
	}

	// Connect to the peer
	if err := host.Connect(ctx, *peerInfo); err != nil {
		return fmt.Errorf("failed to connect to peer: %w", err)
	}

	return nil
}

// DisconnectFromPeer disconnects from a specific peer
func (n *NetworkInfoImpl) DisconnectFromPeer(ctx context.Context, peerID string) error {
	if !n.client.isConnected() {
		return fmt.Errorf("client not connected")
	}

	if err := n.client.requireAccess(ctx); err != nil {
		return fmt.Errorf("authentication required: %w - run CLI commands to authenticate automatically", err)
	}

	host := n.client.host
	if host == nil {
		return fmt.Errorf("no host available")
	}

	// Parse the peer ID
	pid, err := peer.Decode(peerID)
	if err != nil {
		return fmt.Errorf("invalid peer ID: %w", err)
	}

	// Close the connection to the peer
	if err := host.Network().ClosePeer(pid); err != nil {
		return fmt.Errorf("failed to disconnect from peer: %w", err)
	}

	return nil
}
