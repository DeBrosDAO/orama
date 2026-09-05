package install

import (
	"regexp"
	"strings"

	"github.com/DeBrosOfficial/network/pkg/cli/clierr"
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

	// Remote drives the install over SSH against VpsIP instead of installing
	// on this machine. This used to be inferred from whether the process was
	// root, so the same command line meant two different things.
	Remote bool

	// Operator metadata (set by orama node setup, written to node.yaml for registration)
	SSHUser        string // SSH user for remote management
	Environment    string // Environment name (devnet, testnet, etc.)
	OperatorWallet string // Operator wallet address
}

// ParseFlags parses install command flags

// operatorWalletPattern is a 0x-prefixed 20-byte EVM address.
var operatorWalletPattern = regexp.MustCompile(`^0x[0-9a-fA-F]{40}$`)

// validateOperatorWallet refuses an --operator-wallet that is not an address.
//
// The value used to be a free-form string written into node.yaml and echoed
// into a dns_nodes column, so a typo produced a node nobody owned and nothing
// said so. It seeds the cluster's operator list now (migration 044), and an
// operator list built from typos is an operator list nobody is on.
func (f *Flags) validateOperatorWallet() error {
	wallet := strings.TrimSpace(f.OperatorWallet)
	if wallet == "" {
		return nil
	}
	if !operatorWalletPattern.MatchString(wallet) {
		return clierr.Usage("--operator-wallet %q is not a wallet address: expected 0x "+
			"followed by 40 hex characters.\n"+
			"  This address becomes an operator of the cluster, so a typo means "+
			"nobody can mint an invite or list nodes.", wallet)
	}
	f.OperatorWallet = strings.ToLower(wallet)
	return nil
}
