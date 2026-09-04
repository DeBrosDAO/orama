package install

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/DeBrosOfficial/network/pkg/cli"
	"github.com/DeBrosOfficial/network/pkg/cli/noderesolver"
	"github.com/DeBrosOfficial/network/pkg/cli/remotessh"
	"github.com/DeBrosOfficial/network/pkg/inspector"
)

// RemoteOrchestrator orchestrates a remote install via SSH.
// It uploads the source archive, extracts it on the VPS, and runs
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

// Execute runs the remote install process.
// If a binary archive exists locally, uploads and extracts it on the VPS
// so Phase2b auto-detects pre-built mode. Otherwise, source must already
// be present on the VPS.
func (r *RemoteOrchestrator) Execute() error {
	defer r.cleanup()

	fmt.Printf("Installing on %s via SSH (%s@%s)...\n\n", r.flags.VpsIP, r.node.User, r.node.Host)

	// Try to upload a binary archive if one exists locally
	if err := r.uploadBinaryArchive(); err != nil {
		fmt.Printf("  Binary archive upload skipped: %v\n", err)
		fmt.Printf("  Proceeding with source mode (source must already be on VPS)\n\n")
	}

	// Run remote install
	fmt.Printf("Running install on VPS...\n\n")
	if err := r.runRemoteInstall(); err != nil {
		return err
	}

	return nil
}

// uploadBinaryArchive finds a local binary archive and uploads + extracts it on the VPS.
// Returns nil on success, error if no archive found or upload failed.
func (r *RemoteOrchestrator) uploadBinaryArchive() error {
	archivePath := r.findLocalArchive()
	if archivePath == "" {
		return fmt.Errorf("no binary archive found locally")
	}

	fmt.Printf("Uploading binary archive: %s\n", filepath.Base(archivePath))

	// Upload to /tmp/ on VPS
	remoteTmp := "/tmp/" + filepath.Base(archivePath)
	if err := remotessh.UploadFile(r.node, archivePath, remoteTmp); err != nil {
		return fmt.Errorf("failed to upload archive: %w", err)
	}

	// Extract to /opt/orama/ and install CLI to PATH
	fmt.Printf("Extracting archive on VPS...\n")
	extractCmd := fmt.Sprintf("%smkdir -p /opt/orama && tar xzf %s -C /opt/orama && rm -f %s && cp /opt/orama/bin/orama /usr/local/bin/orama && chmod +x /usr/local/bin/orama && echo '  ✓ Archive extracted, CLI installed'",
		r.sudoPrefix(), remoteTmp, remoteTmp)
	if err := remotessh.RunSSHStreaming(r.node, extractCmd); err != nil {
		return fmt.Errorf("failed to extract archive on VPS: %w", err)
	}

	fmt.Println()
	return nil
}

// findLocalArchive searches for a binary archive in common locations.
func (r *RemoteOrchestrator) findLocalArchive() string {
	// Check /tmp/ for archives matching the naming pattern
	entries, err := os.ReadDir("/tmp")
	if err != nil {
		return ""
	}

	// Look for orama-*-linux-*.tar.gz, prefer newest
	var best string
	var bestMod int64
	for _, entry := range entries {
		name := entry.Name()
		if strings.HasPrefix(name, "orama-") && strings.Contains(name, "-linux-") && strings.HasSuffix(name, ".tar.gz") {
			info, err := entry.Info()
			if err != nil {
				continue
			}
			if info.ModTime().Unix() > bestMod {
				best = filepath.Join("/tmp", name)
				bestMod = info.ModTime().Unix()
			}
		}
	}

	return best
}

// runRemoteInstall executes `orama node install` on the VPS.
func (r *RemoteOrchestrator) runRemoteInstall() error {
	cmd := r.buildRemoteCommand()
	return remotessh.RunSSHStreaming(r.node, cmd)
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
	args = append(args, "orama", "node", "install")
	args = append(args, remoteInstallArgs(r.flags)...)

	return joinShellArgs(args)
}

// remoteInstallArgs renders the flags to forward to the node.
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
		{"token", flags.Token},
		{"ca-fingerprint", flags.CAFingerprint},
		{"ssh-user", flags.SSHUser},
		{"environment", flags.Environment},
		{"operator-wallet", flags.OperatorWallet},
		{"peers", flags.PeersStr},
		{"ipfs-peer", flags.IPFSPeerID},
		{"ipfs-addrs", flags.IPFSAddrs},
		{"ipfs-cluster-peer", flags.IPFSClusterPeerID},
		{"ipfs-cluster-addrs", flags.IPFSClusterAddrs},
		{"cluster-secret", flags.ClusterSecret},
		{"swarm-key", flags.SwarmKey},
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
		{"anyone-client", flags.AnyoneClient},
	}
	for _, f := range boolFlags {
		if f.set {
			args = append(args, "--"+f.name)
		}
	}

	return args
}

// sudoPrefix returns "sudo " for non-root SSH users, empty for root.
func (r *RemoteOrchestrator) sudoPrefix() string {
	if r.node.User == "root" {
		return ""
	}
	return "sudo "
}

// joinShellArgs joins arguments, quoting those with special characters.
func joinShellArgs(args []string) string {
	var parts []string
	for _, a := range args {
		if needsQuoting(a) {
			parts = append(parts, "'"+a+"'")
		} else {
			parts = append(parts, a)
		}
	}
	return strings.Join(parts, " ")
}

// needsQuoting returns true if the string contains characters
// that need shell quoting.
func needsQuoting(s string) bool {
	for _, c := range s {
		switch c {
		case ' ', '$', '!', '&', '(', ')', '<', '>', '|', ';', '"', '`', '\\', '#', '^', '*', '?', '{', '}', '[', ']', '~':
			return true
		}
	}
	return false
}
