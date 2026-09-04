package install

// Flags represents install command flags
type Flags struct {
	VpsIP         string
	Domain        string
	BaseDomain    string // Base domain for deployment routing (e.g., "dbrs.space")
	Force         bool
	DryRun        bool
	SkipChecks    bool
	Nameserver    bool   // Make this node a nameserver (runs CoreDNS + Caddy)
	JoinAddress   string // HTTPS URL of existing node (e.g., https://node1.dbrs.space)
	Token         string // Invite token for joining (from orama node invite)
	ClusterSecret string // Deprecated: use --token instead
	SwarmKey      string // Deprecated: use --token instead
	PeersStr      string // Deprecated: use --token instead

	// IPFS/Cluster specific info for Peering configuration
	IPFSPeerID        string
	IPFSAddrs         string
	IPFSClusterPeerID string
	IPFSClusterAddrs  string

	// Security flags
	SkipFirewall  bool   // Skip UFW firewall setup (for users who manage their own firewall)
	CAFingerprint string // SHA-256 fingerprint of server TLS cert for TOFU verification

	// Anyone flags
	AnyoneClient bool // Run Anyone as client-only (SOCKS5 proxy on port 9050, no relay)

	// Operator metadata (set by orama node setup, written to node.yaml for registration)
	SSHUser        string // SSH user for remote management
	Environment    string // Environment name (devnet, testnet, etc.)
	OperatorWallet string // Operator wallet address
}

// ParseFlags parses install command flags
