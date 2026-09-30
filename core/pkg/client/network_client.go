package client

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/DeBrosOfficial/network/pkg/constants"
	"github.com/DeBrosOfficial/network/pkg/ipfs"
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
	n.client.mu.RLock()
	host := n.client.host
	n.client.mu.RUnlock()
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

	n.client.mu.RLock()
	host := n.client.host
	dbClient := n.client.database
	directDatabase := n.client.usesRQLiteEndpoints()
	n.client.mu.RUnlock()
	if host == nil {
		return nil, fmt.Errorf("no host available")
	}

	// Get actual network status
	connectedPeers := host.Network().Peers()

	// Try to get database size from RQLite (optional - don't fail if unavailable)
	// A client that reaches its database through a gateway has no RQLite
	// connection to size.
	var dbSize int64 = 0
	if directDatabase {
		dbSize = rqliteDatabaseSize(dbClient)
	}

	// Try to get IPFS peer info (optional - don't fail if unavailable)
	ipfsInfo := queryIPFSPeerInfo()

	// Try to get IPFS Cluster peer info (optional - don't fail if unavailable)
	ipfsClusterInfo := queryIPFSClusterPeerInfo(constants.LocalIPFSClusterURL(), n.client.config.IPFSClusterAPIPassword)

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
	// IPFS API runs on constants.IPFSAPIPort in our setup
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	resp, err := ipfs.LocalPostAPI(ctx, fmt.Sprintf("http://localhost:%d/api/v0/id", constants.IPFSAPIPort))
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

// queryIPFSClusterPeerInfo queries this node's IPFS Cluster REST API at apiURL
// for its peer information, with the API's basic-auth password
// (ipfs.ClusterRESTPassword). Returns nil if IPFS Cluster is not running or
// unavailable.
func queryIPFSClusterPeerInfo(apiURL, password string) *IPFSClusterPeerInfo {
	client := &http.Client{Timeout: 2 * time.Second}
	req, err := http.NewRequest(http.MethodGet, apiURL+"/id", nil)
	if err != nil {
		return nil
	}
	req.SetBasicAuth(ipfs.ClusterRESTUser, password)
	resp, err := client.Do(req)
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
		return fmt.Errorf("%w: %q is not a multiaddr: %v", ErrInvalidPeer, peerAddr, err)
	}

	// Extract peer info
	peerInfo, err := peer.AddrInfoFromP2pAddr(ma)
	if err != nil {
		return fmt.Errorf("%w: %q must end in /p2p/<peer id>: %v", ErrInvalidPeer, peerAddr, err)
	}

	if len(peerInfo.Addrs) == 0 {
		return fmt.Errorf("%w: %q names a peer but no address to dial it at", ErrInvalidPeer, peerAddr)
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
		return fmt.Errorf("%w: %q is not a peer ID: %v", ErrInvalidPeer, peerID, err)
	}

	// Close the connection to the peer
	if err := host.Network().ClosePeer(pid); err != nil {
		return fmt.Errorf("failed to disconnect from peer: %w", err)
	}

	return nil
}

// rqliteDatabaseSize is the database's size in bytes, 0 when it cannot be read.
func rqliteDatabaseSize(dbClient *DatabaseClientImpl) int64 {
	conn, err := dbClient.getRQLiteConnection()
	if err != nil {
		return 0
	}
	result, err := conn.QueryOne("SELECT page_count * page_size as size FROM pragma_page_count(), pragma_page_size()")
	if err != nil {
		return 0
	}
	var size int64
	for result.Next() {
		if row, err := result.Slice(); err == nil && len(row) > 0 {
			if n, ok := row[0].(int64); ok {
				size = n
			}
		}
	}
	return size
}
