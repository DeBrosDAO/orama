package discovery

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/DeBrosOfficial/network/pkg/tlsutil"
)

// DiscoveryResult contains all information discovered from a peer node
type DiscoveryResult struct {
	PeerID         string   // LibP2P peer ID
	IPFSPeerID     string   // IPFS peer ID
	IPFSSwarmAddrs []string // IPFS swarm addresses
	// IPFS Cluster info for cluster peer discovery
	IPFSClusterPeerID string   // IPFS Cluster peer ID
	IPFSClusterAddrs  []string // IPFS Cluster multiaddresses
}

// DiscoverPeerFromDomain queries an existing node to get its peer ID and IPFS info
// Tries HTTPS first, then falls back to HTTP
// Respects DEBROS_TRUSTED_TLS_DOMAINS and DEBROS_CA_CERT_PATH environment variables for certificate verification
func DiscoverPeerFromDomain(domain string) (*DiscoveryResult, error) {
	// Use centralized TLS configuration that respects CA certificates and trusted domains
	client := tlsutil.NewHTTPClientForDomain(10*time.Second, domain)

	// Try HTTPS first
	url := fmt.Sprintf("https://%s/v1/network/status", domain)
	resp, err := client.Get(url)

	// If HTTPS fails, try HTTP
	if err != nil {
		// Finally try plain HTTP
		url = fmt.Sprintf("http://%s/v1/network/status", domain)
		resp, err = client.Get(url)
		if err != nil {
			return nil, fmt.Errorf("could not connect to %s (tried HTTPS and HTTP): %w", domain, err)
		}
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status from %s: %s", domain, resp.Status)
	}

	// Parse response including IPFS and IPFS Cluster info
	var status struct {
		PeerID string `json:"peer_id"`
		NodeID string `json:"node_id"` // fallback for backward compatibility
		IPFS   *struct {
			PeerID         string   `json:"peer_id"`
			SwarmAddresses []string `json:"swarm_addresses"`
		} `json:"ipfs,omitempty"`
		IPFSCluster *struct {
			PeerID    string   `json:"peer_id"`
			Addresses []string `json:"addresses"`
		} `json:"ipfs_cluster,omitempty"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&status); err != nil {
		return nil, fmt.Errorf("failed to parse response from %s: %w", domain, err)
	}

	// Use peer_id if available, otherwise fall back to node_id for backward compatibility
	peerID := status.PeerID
	if peerID == "" {
		peerID = status.NodeID
	}

	if peerID == "" {
		return nil, fmt.Errorf("no peer_id or node_id in response from %s", domain)
	}

	result := &DiscoveryResult{
		PeerID: peerID,
	}

	// Include IPFS info if available
	if status.IPFS != nil {
		result.IPFSPeerID = status.IPFS.PeerID
		result.IPFSSwarmAddrs = status.IPFS.SwarmAddresses
	}

	// Include IPFS Cluster info if available
	if status.IPFSCluster != nil {
		result.IPFSClusterPeerID = status.IPFSCluster.PeerID
		result.IPFSClusterAddrs = status.IPFSCluster.Addresses
	}

	return result, nil
}
