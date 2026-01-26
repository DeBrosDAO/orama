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
	JoinAddress   string
	ClusterSecret string
	SwarmKey      string
	PeersStr      string

	// IPFS/Cluster specific info for Peering configuration
	IPFSPeerID        string
	IPFSAddrs         string
	IPFSClusterPeerID string
	IPFSClusterAddrs  string
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
	fs.StringVar(&flags.JoinAddress, "join", "", "Join an existing cluster (e.g. 1.2.3.4:7001)")
	fs.StringVar(&flags.ClusterSecret, "cluster-secret", "", "Cluster secret for IPFS Cluster (required if joining)")
	fs.StringVar(&flags.SwarmKey, "swarm-key", "", "IPFS Swarm key (required if joining)")
	fs.StringVar(&flags.PeersStr, "peers", "", "Comma-separated list of bootstrap peer multiaddrs")

	// IPFS/Cluster specific info for Peering configuration
	fs.StringVar(&flags.IPFSPeerID, "ipfs-peer", "", "Peer ID of existing IPFS node to peer with")
	fs.StringVar(&flags.IPFSAddrs, "ipfs-addrs", "", "Comma-separated multiaddrs of existing IPFS node")
	fs.StringVar(&flags.IPFSClusterPeerID, "ipfs-cluster-peer", "", "Peer ID of existing IPFS Cluster node")
	fs.StringVar(&flags.IPFSClusterAddrs, "ipfs-cluster-addrs", "", "Comma-separated multiaddrs of existing IPFS Cluster node")

	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return nil, err
		}
		return nil, fmt.Errorf("failed to parse flags: %w", err)
	}

	return flags, nil
}
