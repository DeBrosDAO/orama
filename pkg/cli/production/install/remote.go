package install

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/DeBrosOfficial/network/pkg/inspector"
)

// RemoteOrchestrator orchestrates a remote install via SSH.
// It uploads the source archive, extracts it on the VPS, and runs
// the actual install command remotely.
type RemoteOrchestrator struct {
	flags *Flags
	node  inspector.Node
}

// NewRemoteOrchestrator creates a new remote orchestrator.
// It resolves SSH credentials and checks prerequisites.
func NewRemoteOrchestrator(flags *Flags) (*RemoteOrchestrator, error) {
	if flags.VpsIP == "" {
		return nil, fmt.Errorf("--vps-ip is required\nExample: orama install --vps-ip 1.2.3.4 --nameserver --domain orama-testnet.network")
	}

	// Resolve SSH credentials
	node, err := resolveSSHCredentials(flags.VpsIP)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve SSH credentials: %w", err)
	}

	return &RemoteOrchestrator{
		flags: flags,
		node:  node,
	}, nil
}

// Execute runs the remote install process.
// If a binary archive exists locally, uploads and extracts it on the VPS
// so Phase2b auto-detects pre-built mode. Otherwise, source must already
// be present on the VPS.
func (r *RemoteOrchestrator) Execute() error {
	fmt.Printf("Installing on %s via SSH (%s@%s)...\n\n", r.flags.VpsIP, r.node.User, r.node.Host)

	// Try to upload a binary archive if one exists locally
	if err := r.uploadBinaryArchive(); err != nil {
		fmt.Printf("  ⚠️  Binary archive upload skipped: %v\n", err)
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
	if err := uploadFile(r.node, archivePath, remoteTmp); err != nil {
		return fmt.Errorf("failed to upload archive: %w", err)
	}

	// Extract to /opt/orama/ and install CLI to PATH
	fmt.Printf("Extracting archive on VPS...\n")
	extractCmd := fmt.Sprintf("%smkdir -p /opt/orama && tar xzf %s -C /opt/orama && rm -f %s && cp /opt/orama/bin/orama /usr/local/bin/orama && chmod +x /usr/local/bin/orama && echo '  ✓ Archive extracted, CLI installed'",
		r.sudoPrefix(), remoteTmp, remoteTmp)
	if err := runSSHStreaming(r.node, extractCmd); err != nil {
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

// runRemoteInstall executes `orama install` on the VPS.
func (r *RemoteOrchestrator) runRemoteInstall() error {
	cmd := r.buildRemoteCommand()
	return runSSHStreaming(r.node, cmd)
}

// buildRemoteCommand constructs the `sudo orama install` command string
// with all flags passed through.
func (r *RemoteOrchestrator) buildRemoteCommand() string {
	var args []string
	if r.node.User != "root" {
		args = append(args, "sudo")
	}
	args = append(args, "orama", "node", "install")

	args = append(args, "--vps-ip", r.flags.VpsIP)

	if r.flags.Domain != "" {
		args = append(args, "--domain", r.flags.Domain)
	}
	if r.flags.BaseDomain != "" {
		args = append(args, "--base-domain", r.flags.BaseDomain)
	}
	if r.flags.Nameserver {
		args = append(args, "--nameserver")
	}
	if r.flags.JoinAddress != "" {
		args = append(args, "--join", r.flags.JoinAddress)
	}
	if r.flags.Token != "" {
		args = append(args, "--token", r.flags.Token)
	}
	if r.flags.Force {
		args = append(args, "--force")
	}
	if r.flags.SkipChecks {
		args = append(args, "--skip-checks")
	}
	if r.flags.SkipFirewall {
		args = append(args, "--skip-firewall")
	}
	if r.flags.DryRun {
		args = append(args, "--dry-run")
	}

	// Anyone relay flags
	if r.flags.AnyoneRelay {
		args = append(args, "--anyone-relay")
	}
	if r.flags.AnyoneClient {
		args = append(args, "--anyone-client")
	}
	if r.flags.AnyoneExit {
		args = append(args, "--anyone-exit")
	}
	if r.flags.AnyoneMigrate {
		args = append(args, "--anyone-migrate")
	}
	if r.flags.AnyoneNickname != "" {
		args = append(args, "--anyone-nickname", r.flags.AnyoneNickname)
	}
	if r.flags.AnyoneContact != "" {
		args = append(args, "--anyone-contact", r.flags.AnyoneContact)
	}
	if r.flags.AnyoneWallet != "" {
		args = append(args, "--anyone-wallet", r.flags.AnyoneWallet)
	}
	if r.flags.AnyoneORPort != 9001 {
		args = append(args, "--anyone-orport", strconv.Itoa(r.flags.AnyoneORPort))
	}
	if r.flags.AnyoneFamily != "" {
		args = append(args, "--anyone-family", r.flags.AnyoneFamily)
	}
	if r.flags.AnyoneBandwidth != 30 {
		args = append(args, "--anyone-bandwidth", strconv.Itoa(r.flags.AnyoneBandwidth))
	}
	if r.flags.AnyoneAccounting != 0 {
		args = append(args, "--anyone-accounting", strconv.Itoa(r.flags.AnyoneAccounting))
	}

	return joinShellArgs(args)
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
