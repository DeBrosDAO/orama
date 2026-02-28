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

	// Global RQLite DSN for API key validation (for namespace gateways)
	// If empty, uses RQLiteDSN (for main/global gateways)
	GlobalRQLiteDSN string

	// HTTPS configuration
	EnableHTTPS bool   // Enable HTTPS with ACME (Let's Encrypt)
	DomainName  string // Domain name for HTTPS certificate
	TLSCacheDir string // Directory to cache TLS certificates (default: ~/.orama/tls-cache)

	// Domain routing configuration
	BaseDomain string // Base domain for deployment routing. Set via node config http_gateway.base_domain. Defaults to "dbrs.space"

	// Data directory configuration
	DataDir string // Base directory for node-local data (SQLite databases, deployments). Defaults to ~/.orama

	// Olric cache configuration
	OlricServers []string      // List of Olric server addresses (e.g., ["localhost:3320"]). If empty, defaults to ["localhost:3320"]
	OlricTimeout time.Duration // Timeout for Olric operations (default: 10s)

	// IPFS Cluster configuration
	IPFSClusterAPIURL     string        // IPFS Cluster HTTP API URL (e.g., "http://localhost:9094"). If empty, gateway will discover from node configs
	IPFSAPIURL            string        // IPFS HTTP API URL for content retrieval (e.g., "http://localhost:4501"). If empty, gateway will discover from node configs
	IPFSTimeout           time.Duration // Timeout for IPFS operations (default: 60s)
	IPFSReplicationFactor int           // Replication factor for pins (default: 3)
	IPFSEnableEncryption  bool          // Enable client-side encryption before upload (default: true, discovered from node configs)

	// RQLite authentication (basic auth credentials embedded in DSN)
	RQLiteUsername string // RQLite HTTP basic auth username (default: "orama")
	RQLitePassword string // RQLite HTTP basic auth password

	// WireGuard mesh configuration
	ClusterSecret string // Cluster secret for authenticating internal WireGuard peer exchange

	// API key HMAC secret for hashing API keys before storage.
	// When set, API keys are stored as HMAC-SHA256(key, secret) in the database.
	// Loaded from ~/.orama/secrets/api-key-hmac-secret.
	APIKeyHMACSecret string

	// WebRTC configuration (set when namespace has WebRTC enabled)
	WebRTCEnabled bool   // Whether WebRTC endpoints are active on this gateway
	SFUPort       int    // Local SFU signaling port to proxy WebSocket connections to
	TURNDomain    string // TURN server domain for credential generation
	TURNSecret    string // HMAC-SHA1 shared secret for TURN credential generation
}
