package install

import (
	"bytes"
	"fmt"
	"strings"

	"github.com/DeBrosOfficial/network/cmd/orama/internal/clierr"
	"github.com/DeBrosOfficial/network/cmd/orama/internal/production/clusterops"
	"github.com/DeBrosOfficial/network/cmd/orama/internal/production/setup"

	"github.com/DeBrosOfficial/network/cmd/orama/internal"
	"github.com/DeBrosOfficial/network/cmd/orama/internal/noderesolver"
	"github.com/DeBrosOfficial/network/pkg/inspector"
	"github.com/DeBrosOfficial/network/pkg/remotessh"
)

// installedArchiveCLI is the orama binary in the archive extracted at
// /opt/orama, which runs the install on a machine that has no other.
const installedArchiveCLI = "/opt/orama/bin/orama"

// RemoteOrchestrator orchestrates a remote install via SSH.
// It uploads the verified build archive, extracts it on the VPS, and runs
// the actual install command remotely.
type RemoteOrchestrator struct {
	flags   *Flags
	node    inspector.Node
	cleanup func()
}

// NewRemoteOrchestrator creates a new remote orchestrator.
// Resolves SSH credentials via wallet-derived keys and checks prerequisites.
func NewRemoteOrchestrator(flags *Flags) (*RemoteOrchestrator, error) {
	if flags.VpsIP == "" {
		return nil, fmt.Errorf("--vps-ip is required\nExample: orama node install --vps-ip 1.2.3.4 --nameserver --domain orama-testnet.network")
	}

	node := resolveTarget(flags.VpsIP)

	// Prepare wallet-derived SSH key
	nodes := []inspector.Node{node}
	cleanup, err := remotessh.PrepareNodeKeys(nodes)
	if err != nil {
		return nil, fmt.Errorf("failed to prepare SSH key: %w\nEnsure you've run: rw vault ssh add %s/%s", err, node.Host, node.User)
	}
	// PrepareNodeKeys modifies nodes in place
	node = nodes[0]

	return &RemoteOrchestrator{
		flags:   flags,
		node:    node,
		cleanup: cleanup,
	}, nil
}

// resolveTarget describes the machine to install on.
//
// A node being installed is usually not in the inventory yet, which is the
// point of the command, so the inventory is consulted only to pick up a
// non-default SSH user for a machine that is already registered. This used to
// read nodes.conf directly, a fourth node-lookup path that disagreed with the
// resolver every other command uses.
func resolveTarget(vpsIP string) inspector.Node {
	env := ""
	if active, err := cli.GetActiveEnvironment(); err == nil {
		env = active.Name
	}

	node := noderesolver.NewNode(vpsIP, "", env)
	node.Role = "node"

	known, err := noderesolver.ResolveNodes(env)
	if err != nil {
		return node
	}
	for _, n := range known {
		if n.Host == vpsIP {
			n.Role = "node"
			return n
		}
	}
	return node
}

// Execute runs the remote install: it verifies the archive here, uploads the
// verified copy, and runs the install on the VPS with the binary from it.
//
// It used to upload the newest /tmp/orama-*-linux-*.tar.gz it could find —
// another user's or another checkout's build as often as not — extract it with
// no check at all, and, when there was none, go on in "source mode" and
// compile whatever /opt/orama/src held.
func (r *RemoteOrchestrator) Execute() error {
	defer r.cleanup()

	trusted, err := remoteArchiveSigners(r.flags)
	if err != nil {
		return err
	}
	fmt.Printf("Installing on %s via SSH (%s@%s)...\n\n", r.flags.VpsIP, r.node.User, r.node.Host)
	if err := setup.EnsureArchive(r.node, r.flags.Archive, trusted); err != nil {
		return err
	}

	fmt.Printf("Running install on VPS...\n\n")
	return r.runRemoteInstall()
}

// remoteArchiveSigners is who the archive a --remote install uploads must be
// signed by: the operator wallet, which a genesis node also makes its trust
// anchor and which the join tags the node with.
func remoteArchiveSigners(flags *Flags) ([]string, error) {
	if flags.Archive == "" {
		return nil, clierr.Usage("--remote needs --archive <path>: the build to install, as `orama build` printed it")
	}
	if flags.OperatorWallet == "" {
		return nil, clierr.Usage("--remote verifies the archive on this machine before uploading it, against " +
			"--operator-wallet: pass the wallet that signed the build")
	}
	return []string{flags.OperatorWallet}, nil
}

// runRemoteInstall executes `orama node install` on the VPS, with the
// secrets on its stdin rather than its command line (secrets_stdin.go).
func (r *RemoteOrchestrator) runRemoteInstall() error {
	secrets, err := remoteSecrets(r.flags)
	if err != nil {
		return err
	}
	cmd := r.buildRemoteCommand()
	if secrets == nil {
		return remotessh.RunSSHStreaming(r.node, cmd)
	}
	return remotessh.RunSSHStreaming(r.node, cmd, remotessh.WithStdin(bytes.NewReader(secrets)))
}

// buildRemoteCommand constructs the `sudo orama node install` command line.
//
// Every flag the operator gave has to reach the node, and the list used to be
// written out by hand and had drifted: --ca-fingerprint, --environment,
// --ssh-user, --operator-wallet, --peers and the four --ipfs-* flags were
// silently dropped. Dropping --ca-fingerprint is the one that matters —
// without it the joining node has nothing to pin the cluster's certificate
// against and falls back to trust-on-first-use, so a laptop-driven join
// quietly did not get the verification the operator asked for. The others
// meant a node registered with no environment, no SSH user and no owner.
//
// remoteInstallArgs is the list, so a new install flag is added in one place
// and a guard test can check none is missing.
func (r *RemoteOrchestrator) buildRemoteCommand() string {
	var args []string
	if r.node.User != "root" {
		args = append(args, "sudo")
	}
	// The binary from the uploaded, verified archive: the node has no other.
	args = append(args, installedArchiveCLI, "node", "install")
	args = append(args, remoteInstallArgs(r.flags)...)

	return joinShellArgs(args)
}

// remoteInstallArgs renders the flags to forward to the node. The secrets
// are not among them: they go on the command's stdin (remoteSecrets), and the
// command line says only --secrets-stdin.
func remoteInstallArgs(flags *Flags) []string {
	var args []string

	strFlags := []struct {
		name  string
		value string
	}{
		{"vps-ip", flags.VpsIP},
		{"domain", flags.Domain},
		{"base-domain", flags.BaseDomain},
		{"join", flags.JoinAddress},
		{"ca-fingerprint", flags.CAFingerprint},
		{"join-sni", flags.JoinSNI},
		{"ssh-user", flags.SSHUser},
		{"environment", flags.Environment},
		{"operator-wallet", flags.OperatorWallet},
		{"expect-archive-signers", flags.ExpectArchiveSigners},
		{"acme-ca", flags.ACMECA},
		{"peers", flags.PeersStr},
		{"ipfs-peer", flags.IPFSPeerID},
		{"ipfs-addrs", flags.IPFSAddrs},
		{"ipfs-cluster-peer", flags.IPFSClusterPeerID},
		{"ipfs-cluster-addrs", flags.IPFSClusterAddrs},
	}
	for _, f := range strFlags {
		if f.value != "" {
			args = append(args, "--"+f.name, f.value)
		}
	}

	boolFlags := []struct {
		name string
		set  bool
	}{
		{"nameserver", flags.Nameserver},
		{"force", flags.Force},
		{"skip-checks", flags.SkipChecks},
		{"skip-firewall", flags.SkipFirewall},
		{"dry-run", flags.DryRun},
		{secretsStdinFlag, flags.Token != "" || flags.ClusterSecret != "" || flags.SwarmKey != ""},
	}
	for _, f := range boolFlags {
		if f.set {
			args = append(args, "--"+f.name)
		}
	}

	return args
}

// joinShellArgs joins arguments into one command line for the remote root
// shell. A plain word goes as it is; anything else is single-quoted with its
// own quotes escaped. It used to wrap in quotes without escaping them, and did
// not count a newline as special, so an invite field (SNI, URL, token) carrying
// a quote or a newline ran as root on the joining machine.
func joinShellArgs(args []string) string {
	parts := make([]string, 0, len(args))
	for _, a := range args {
		if isPlainShellWord(a) {
			parts = append(parts, a)
		} else {
			parts = append(parts, clusterops.ShellQuote(a))
		}
	}
	return strings.Join(parts, " ")
}

// isPlainShellWord reports whether s needs no quoting: non-empty and made only
// of characters no POSIX shell treats specially.
func isPlainShellWord(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		case strings.ContainsRune("-_./:=@,+%", c):
		default:
			return false
		}
	}
	return true
}
