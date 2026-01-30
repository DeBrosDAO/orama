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
	Branch        string
	NoPull        bool
	Force         bool
	DryRun        bool
	SkipChecks    bool
	Nameserver    bool   // Make this node a nameserver (runs CoreDNS + Caddy)
	JoinAddress   string // HTTPS URL of existing node (e.g., https://node1.dbrs.space)
	Token         string // Invite token for joining (from orama invite)
	ClusterSecret string // Deprecated: use --token instead
	SwarmKey      string // Deprecated: use --token instead
	PeersStr      string // Deprecated: use --token instead

	// IPFS/Cluster specific info for Peering configuration
	IPFSPeerID        string
	IPFSAddrs         string
	IPFSClusterPeerID string
	IPFSClusterAddrs  string

	// Security flags
	SkipFirewall bool // Skip UFW firewall setup (for users who manage their own firewall)

	// Anyone relay operator flags
	AnyoneRelay    bool   // Run as relay operator instead of client
	AnyoneExit     bool   // Run as exit relay (legal implications)
	AnyoneMigrate  bool   // Migrate existing Anyone installation
	AnyoneNickname string // Relay nickname (1-19 alphanumeric)
	AnyoneContact  string // Contact info (email or @telegram)
	AnyoneWallet   string // Ethereum wallet for rewards
	AnyoneORPort   int    // ORPort for relay (default 9001)
	AnyoneFamily   string // Comma-separated fingerprints of other relays you operate
}

// ParseFlags parses install command flags
func ParseFlags(args []string) (*Flags, error) {
	fs := flag.NewFlagSet("install", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)

	flags := &Flags{}

	fs.StringVar(&flags.VpsIP, "vps-ip", "", "Public IP of this VPS (required)")
	fs.StringVar(&flags.Domain, "domain", "", "Domain name for HTTPS (optional, e.g. gateway.example.com)")
	fs.StringVar(&flags.BaseDomain, "base-domain", "", "Base domain for deployment routing (e.g., dbrs.space)")
	fs.StringVar(&flags.Branch, "branch", "main", "Git branch to use (main or nightly)")
	fs.BoolVar(&flags.NoPull, "no-pull", false, "Skip git clone/pull, use existing repository in /home/debros/src")
	fs.BoolVar(&flags.Force, "force", false, "Force reconfiguration even if already installed")
	fs.BoolVar(&flags.DryRun, "dry-run", false, "Show what would be done without making changes")
	fs.BoolVar(&flags.SkipChecks, "skip-checks", false, "Skip minimum resource checks (RAM/CPU)")
	fs.BoolVar(&flags.Nameserver, "nameserver", false, "Make this node a nameserver (runs CoreDNS + Caddy)")

	// Cluster join flags
	fs.StringVar(&flags.JoinAddress, "join", "", "Join existing cluster via HTTPS URL (e.g. https://node1.dbrs.space)")
	fs.StringVar(&flags.Token, "token", "", "Invite token for joining (from orama invite on existing node)")
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

	// Anyone relay operator flags
	fs.BoolVar(&flags.AnyoneRelay, "anyone-relay", false, "Run as Anyone relay operator (earn rewards)")
	fs.BoolVar(&flags.AnyoneExit, "anyone-exit", false, "Run as exit relay (requires --anyone-relay, legal implications)")
	fs.BoolVar(&flags.AnyoneMigrate, "anyone-migrate", false, "Migrate existing Anyone installation into Orama Network")
	fs.StringVar(&flags.AnyoneNickname, "anyone-nickname", "", "Relay nickname (1-19 alphanumeric chars)")
	fs.StringVar(&flags.AnyoneContact, "anyone-contact", "", "Contact info (email or @telegram)")
	fs.StringVar(&flags.AnyoneWallet, "anyone-wallet", "", "Ethereum wallet address for rewards")
	fs.IntVar(&flags.AnyoneORPort, "anyone-orport", 9001, "ORPort for relay (default 9001)")
	fs.StringVar(&flags.AnyoneFamily, "anyone-family", "", "Comma-separated fingerprints of other relays you operate")

	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return nil, err
		}
		return nil, fmt.Errorf("failed to parse flags: %w", err)
	}

	return flags, nil
}
