package install

import (
	"fmt"
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
// Source must already be uploaded via: ./scripts/upload-source.sh <vps-ip>
func (r *RemoteOrchestrator) Execute() error {
	fmt.Printf("Installing on %s via SSH (%s@%s)...\n\n", r.flags.VpsIP, r.node.User, r.node.Host)

	// Run remote install
	fmt.Printf("Running install on VPS...\n\n")
	if err := r.runRemoteInstall(); err != nil {
		return err
	}

	return nil
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
	args = append(args, "orama", "install")

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
