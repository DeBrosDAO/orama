package gateway

import "time"

// Config holds configuration for the gateway server
type Config struct {
	ListenAddr      string
	ClientNamespace string
	BootstrapPeers  []string
	NodePeerID      string // The node's actual peer ID from its identity file

	// Optional DSN for rqlite database/sql driver, e.g. "http://localhost:4001"
	// If empty, defaults to "http://localhost:4001".
	RQLiteDSN string

	// HTTPS configuration
	EnableHTTPS bool   // Enable HTTPS with ACME (Let's Encrypt)
	DomainName  string // Domain name for HTTPS certificate
	TLSCacheDir string // Directory to cache TLS certificates (default: ~/.orama/tls-cache)

	// Olric cache configuration
	OlricServers []string      // List of Olric server addresses (e.g., ["localhost:3320"]). If empty, defaults to ["localhost:3320"]
	OlricTimeout time.Duration // Timeout for Olric operations (default: 10s)

	// IPFS Cluster configuration
	IPFSClusterAPIURL     string        // IPFS Cluster HTTP API URL (e.g., "http://localhost:9094"). If empty, gateway will discover from node configs
	IPFSAPIURL            string        // IPFS HTTP API URL for content retrieval (e.g., "http://localhost:5001"). If empty, gateway will discover from node configs
	IPFSTimeout           time.Duration // Timeout for IPFS operations (default: 60s)
	IPFSReplicationFactor int           // Replication factor for pins (default: 3)
	IPFSEnableEncryption  bool          // Enable client-side encryption before upload (default: true, discovered from node configs)
}
