package install

import (
	"flag"
	"fmt"
	"os"
)

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
func ParseFlags(args []string) (*Flags, error) {
	fs := flag.NewFlagSet("install", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)

	flags := &Flags{}

	fs.StringVar(&flags.VpsIP, "vps-ip", "", "Public IP of this VPS (required)")
	fs.StringVar(&flags.Domain, "domain", "", "Domain for HTTPS (auto-generated for non-nameserver nodes if omitted)")
	fs.StringVar(&flags.BaseDomain, "base-domain", "", "Base domain for deployment routing (e.g., dbrs.space)")
	fs.BoolVar(&flags.Force, "force", false, "Force reconfiguration even if already installed")
	fs.BoolVar(&flags.DryRun, "dry-run", false, "Show what would be done without making changes")
	fs.BoolVar(&flags.SkipChecks, "skip-checks", false, "Skip minimum resource checks (RAM/CPU)")
	fs.BoolVar(&flags.Nameserver, "nameserver", false, "Make this node a nameserver (runs CoreDNS + Caddy)")

	// Cluster join flags
	fs.StringVar(&flags.JoinAddress, "join", "", "Join existing cluster via HTTPS URL (e.g. https://node1.dbrs.space)")
	fs.StringVar(&flags.Token, "token", "", "Invite token for joining (from orama node invite on existing node)")
	fs.StringVar(&flags.ClusterSecret, "cluster-secret", "", "Deprecated: use --token instead")
	fs.StringVar(&flags.SwarmKey, "swarm-key", "", "Deprecated: use --token instead")
	fs.StringVar(&flags.PeersStr, "peers", "", "Comma-separated list of bootstrap peer multiaddrs")

	// IPFS/Cluster specific info for Peering configuration
	fs.StringVar(&flags.IPFSPeerID, "ipfs-peer", "", "Peer ID of existing IPFS node to peer with")
	fs.StringVar(&flags.IPFSAddrs, "ipfs-addrs", "", "Comma-separated multiaddrs of existing IPFS node")
	fs.StringVar(&flags.IPFSClusterPeerID, "ipfs-cluster-peer", "", "Peer ID of existing IPFS Cluster node")
	fs.StringVar(&flags.IPFSClusterAddrs, "ipfs-cluster-addrs", "", "Comma-separated multiaddrs of existing IPFS Cluster node")

	// Security flags
	fs.BoolVar(&flags.SkipFirewall, "skip-firewall", false, "Skip UFW firewall setup (for users who manage their own firewall)")
	fs.StringVar(&flags.CAFingerprint, "ca-fingerprint", "", "SHA-256 fingerprint of server TLS cert (from orama node invite output)")

	// Anyone flags
	fs.BoolVar(&flags.AnyoneClient, "anyone-client", false, "Install Anyone as client-only (SOCKS5 proxy on port 9050, no relay)")

	// Operator metadata (set by orama node setup)
	fs.StringVar(&flags.SSHUser, "ssh-user", "", "SSH user for remote management")
	fs.StringVar(&flags.Environment, "environment", "", "Environment name (devnet, testnet, etc.)")
	fs.StringVar(&flags.OperatorWallet, "operator-wallet", "", "Operator wallet address")

	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return nil, err
		}
		return nil, fmt.Errorf("failed to parse flags: %w", err)
	}

	return flags, nil
}
