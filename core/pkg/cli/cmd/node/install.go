package node

import (
	"github.com/DeBrosOfficial/network/pkg/cli/production/install"
	"github.com/spf13/cobra"
)

var installFlags install.Flags

var installCmd = &cobra.Command{
	Use:   "install",
	Short: "Install production node (requires sudo)",
	Long: `Install and configure an Orama production node on this machine.
For the first node, this creates a new cluster. For subsequent nodes,
use --join and --token to join an existing cluster.

Run it on the node itself with sudo, or from your own machine with --remote to
drive the install over SSH against --vps-ip. Which of the two happened used to
be decided by whether you had used sudo.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		return install.Run(&installFlags)
	},
}

func init() {
	f := installCmd.Flags()
	f.StringVar(&installFlags.VpsIP, "vps-ip", "", "Public IP of this VPS (required)")
	f.BoolVar(&installFlags.Remote, "remote", false,
		"Install the machine at --vps-ip over SSH, instead of this machine")
	f.StringVar(&installFlags.Domain, "domain", "", "Domain for HTTPS (auto-generated for non-nameserver nodes if omitted)")
	f.StringVar(&installFlags.BaseDomain, "base-domain", "", "Base domain for deployment routing (e.g., dbrs.space)")
	f.BoolVar(&installFlags.Force, "force", false, "Force reconfiguration even if already installed")
	f.BoolVar(&installFlags.DryRun, "dry-run", false, "Show what would be done without making changes")
	f.BoolVar(&installFlags.SkipChecks, "skip-checks", false, "Skip minimum resource checks (RAM/CPU)")
	f.BoolVar(&installFlags.Nameserver, "nameserver", false, "Make this node a nameserver (runs CoreDNS + Caddy)")
	f.StringVar(&installFlags.JoinAddress, "join", "",
		"Gateway to join; the invite carries this, so it is only needed to override it")
	f.StringVar(&installFlags.Token, "token", "",
		"Invite from 'orama invite'; it carries the gateway to join and the certificate to pin")
	f.StringVar(&installFlags.CAFingerprint, "ca-fingerprint", "",
		"SHA-256 fingerprint of the gateway's TLS cert; the invite carries this, so it is only needed to override it")
	f.BoolVar(&installFlags.SkipFirewall, "skip-firewall", false, "Skip UFW firewall setup (for users who manage their own firewall)")
	f.BoolVar(&installFlags.AnyoneClient, "anyone-client", false, "Install Anyone as client-only (SOCKS5 proxy on port 9050, no relay)")
	f.StringVar(&installFlags.SSHUser, "ssh-user", "", "SSH user for remote management")
	f.StringVar(&installFlags.Environment, "environment", "", "Environment name (devnet, testnet, etc.)")
	f.StringVar(&installFlags.OperatorWallet, "operator-wallet", "", "Operator wallet address")

	// Peering details the invite token now carries. Kept for the manual join
	// path and for clusters mid-upgrade.
	f.StringVar(&installFlags.PeersStr, "peers", "", "Comma-separated list of bootstrap peer multiaddrs")
	f.StringVar(&installFlags.IPFSPeerID, "ipfs-peer", "", "Peer ID of existing IPFS node to peer with")
	f.StringVar(&installFlags.IPFSAddrs, "ipfs-addrs", "", "Comma-separated multiaddrs of existing IPFS node")
	f.StringVar(&installFlags.IPFSClusterPeerID, "ipfs-cluster-peer", "", "Peer ID of existing IPFS Cluster node")
	f.StringVar(&installFlags.IPFSClusterAddrs, "ipfs-cluster-addrs", "", "Comma-separated multiaddrs of existing IPFS Cluster node")

	// Superseded by --token; kept so an in-flight upgrade does not break.
	f.StringVar(&installFlags.ClusterSecret, "cluster-secret", "", "Deprecated: use --token instead")
	f.StringVar(&installFlags.SwarmKey, "swarm-key", "", "Deprecated: use --token instead")
	_ = f.MarkDeprecated("cluster-secret", "use --token instead")
	_ = f.MarkDeprecated("swarm-key", "use --token instead")
}
