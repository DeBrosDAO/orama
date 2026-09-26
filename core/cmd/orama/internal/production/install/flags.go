package install

import (
	neturl "net/url"
	"strings"

	"github.com/DeBrosOfficial/network/cmd/orama/internal/clierr"
	"github.com/DeBrosOfficial/network/pkg/archivetrust"
)

// Flags represents install command flags
type Flags struct {
	VpsIP         string
	Domain        string
	BaseDomain    string // Base domain for deployment routing (e.g., "example.com")
	Force         bool
	DryRun        bool
	SkipChecks    bool
	Nameserver    bool   // Make this node a nameserver (runs CoreDNS + Caddy)
	JoinAddress   string // HTTPS URL of existing node (e.g., https://node1.example.com)
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
	// JoinSNI is the server name presented to the join gateway, from the
	// invite: the invite names the minting node by address, and it serves
	// the fingerprinted certificate for this name.
	JoinSNI string

	// SecretsFromStdin makes the install read Token, ClusterSecret and
	// SwarmKey from stdin (secrets_stdin.go). A remote install sets it on the
	// node's command line so the secrets never appear in its argv.
	SecretsFromStdin bool

	// Remote drives the install over SSH against VpsIP instead of installing
	// on this machine. This used to be inferred from whether the process was
	// root, so the same command line meant two different things.
	Remote bool

	// Operator metadata (set by orama node setup, written to node.yaml for registration)
	SSHUser        string // SSH user for remote management
	Environment    string // Environment name (devnet, testnet, etc.)
	OperatorWallet string // Operator wallet address

	// ACMECA is the ACME directory Caddy issues certificates from: an https URL,
	// or letsencrypt-staging. Empty keeps Let's Encrypt production.
	ACMECA string

	// ExpectArchiveSigners (comma-separated) is the archive signer list a
	// joining node expects the cluster to send: the join response must name
	// exactly these, and the archive is verified against them before the join
	// spends the invite. Setup passes the ones it verified the archive with.
	ExpectArchiveSigners string
	// expectedArchiveSigners is ExpectArchiveSigners, normalized.
	expectedArchiveSigners []string

	// Archive is the build archive a --remote install uploads. It is verified
	// on this machine against --operator-wallet first, and never forwarded:
	// the node installs what was uploaded.
	Archive string
}

// acmeCAAliases name ACME directories an operator should not have to paste.
var acmeCAAliases = map[string]string{
	"letsencrypt-staging": "https://acme-staging-v02.api.letsencrypt.org/directory",
}

// resolveACMECA turns an alias into its URL and refuses anything that is not
// an https URL. Test clusters that redeploy from scratch need staging: Let's
// Encrypt allows five certificates per week for the same set of names, and
// every node of a cluster requests the same wildcard.
func (f *Flags) resolveACMECA() error {
	if f.ACMECA == "" {
		return nil
	}
	if url, ok := acmeCAAliases[f.ACMECA]; ok {
		f.ACMECA = url
		return nil
	}
	u, err := neturl.Parse(f.ACMECA)
	if err != nil || u.Scheme != "https" || u.Host == "" || strings.ContainsAny(f.ACMECA, " \t\n{}\"") {
		return clierr.Usage("--acme-ca %q is not an https ACME directory URL or a known alias (letsencrypt-staging)", f.ACMECA)
	}
	return nil
}

// ParseFlags parses install command flags

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
	normalized, err := archivetrust.NormalizeSigners([]string{wallet})
	if err != nil {
		return clierr.Usage("--operator-wallet: %v\n"+
			"  This address becomes an operator of the cluster, and on a genesis node the only "+
			"signer it installs builds from, so a typo means nobody can mint an invite or list nodes.", err)
	}
	f.OperatorWallet = normalized[0]
	return nil
}

// validateExpectedSigners normalizes --expect-archive-signers, which only a
// join uses.
func (f *Flags) validateExpectedSigners() error {
	if f.ExpectArchiveSigners == "" {
		return nil
	}
	if f.JoinAddress == "" || f.Token == "" {
		return clierr.Usage("--expect-archive-signers is for joining a cluster (--join and --token)")
	}
	signers, err := archivetrust.NormalizeSigners(strings.Split(f.ExpectArchiveSigners, ","))
	if err != nil {
		return clierr.Usage("--expect-archive-signers: %v", err)
	}
	f.expectedArchiveSigners = signers
	f.ExpectArchiveSigners = strings.Join(signers, ",")
	return nil
}

// requireGenesisWallet refuses a genesis install without --operator-wallet
// before anything is changed: the wallet becomes the cluster's archive trust
// anchor, and the archive about to be installed must be signed by it.
func (f *Flags) requireGenesisWallet() error {
	joining := f.JoinAddress != "" && f.Token != ""
	if joining || f.DryRun || f.OperatorWallet != "" {
		return nil
	}
	return clierr.Usage("a genesis install needs --operator-wallet: it becomes the only signer this " +
		"cluster accepts build archives from, so the archive in /opt/orama must be signed by it " +
		"(`orama node setup` passes your RootWallet address)")
}
