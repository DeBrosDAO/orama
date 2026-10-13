package sshcmd

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/DeBrosOfficial/network/cmd/orama/internal"
	"github.com/DeBrosOfficial/network/cmd/orama/internal/clierr"
	"github.com/DeBrosOfficial/network/cmd/orama/internal/noderesolver"
	"github.com/DeBrosOfficial/network/pkg/inspector"
	"github.com/DeBrosOfficial/network/pkg/remotessh"
	"github.com/spf13/cobra"
)

var envFlag string

// Cmd is the top-level "ssh" command — SSH into any node by IP or hostname.
var Cmd = &cobra.Command{
	Use:   "ssh <ip-or-hostname> [-- command]",
	Short: "SSH into a node",
	Long: `SSH into a node by IP address or hostname.
Resolves the SSH key from rootwallet automatically.

The node's host key must already be pinned in ~/.orama/known_hosts, where
'orama node setup' writes it. A host with no pinned key is refused, never
trusted on first use, and a key that differs from the pinned one is refused.

Pass a command after the IP to run it non-interactively:
  orama ssh 1.2.3.4 'sudo systemctl status orama-node'`,
	Args:               cobra.MinimumNArgs(1),
	DisableFlagParsing: false,
	RunE: func(cmd *cobra.Command, args []string) error {
		target := args[0]
		remoteCmd := ""
		if len(args) > 1 {
			remoteCmd = args[1]
		}

		env := envFlag
		if env == "" {
			active, err := cli.GetActiveEnvironment()
			if err != nil {
				return fmt.Errorf("failed to get active environment: %w", err)
			}
			env = active.Name
		}

		// Resolve nodes to find the target
		nodes, err := noderesolver.ResolveNodes(env)
		if err != nil {
			return fmt.Errorf("failed to resolve nodes: %w", err)
		}

		// Match by IP
		for _, n := range nodes {
			if n.Host == target {
				return sshInto(n, remoteCmd)
			}
		}

		// Not found — try direct SSH with default vault target
		fmt.Printf("Node %q not found in %s nodes, attempting direct SSH...\n", target, env)
		return sshInto(inspector.Node{
			Host:        target,
			User:        "root",
			VaultTarget: target + "/root",
		}, remoteCmd)
	},
}

func init() {
	Cmd.Flags().StringVar(&envFlag, "env", "", "Environment to search (default: active)")
}

func sshInto(node inspector.Node, remoteCmd string) error {
	if err := requirePinnedHostKey(&node); err != nil {
		return err
	}
	nodes := []inspector.Node{node}
	cleanup, err := remotessh.PrepareNodeKeys(nodes)
	if err != nil {
		return fmt.Errorf("failed to resolve SSH key: %w", err)
	}
	defer cleanup()

	node = nodes[0]

	sshBin, err := exec.LookPath("ssh")
	if err != nil {
		return fmt.Errorf("ssh not found in PATH: %w", err)
	}

	sshCmd := exec.Command(sshBin, sshArgs(node, remoteCmd)...)
	sshCmd.Stdin = os.Stdin
	sshCmd.Stdout = os.Stdout
	sshCmd.Stderr = os.Stderr
	return sshCmd.Run()
}

// sshArgs is the ssh command line for node, whose host key policy is the
// node's own (strict against its pinned file).
func sshArgs(node inspector.Node, remoteCmd string) []string {
	args := append([]string{"-i", node.SSHKey}, node.HostKeyOptions()...)
	args = append(args, "-o", "IdentitiesOnly=yes", fmt.Sprintf("%s@%s", node.User, node.Host))
	if remoteCmd != "" {
		args = append(args, remoteCmd)
	}
	return args
}

// requirePinnedHostKey points node at the host-key pin setup keeps and refuses
// a host with no entry in it. ssh would refuse too under strict checking, but
// only after the wallet key was fetched, and with a message that does not say
// how to pin the host. A node that already names its own known_hosts keeps it.
func requirePinnedHostKey(node *inspector.Node) error {
	if node.KnownHostsFile == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("find the home directory for %s: %w", pinnedHostsFile, err)
		}
		node.KnownHostsFile = filepath.Join(home, pinnedHostsDir, pinnedHostsFile)
	}
	if _, err := os.Stat(node.KnownHostsFile); err != nil {
		return unpinned(node, fmt.Errorf("%s cannot be read: %w", node.KnownHostsFile, err))
	}
	keygen, err := exec.LookPath("ssh-keygen")
	if err != nil {
		return fmt.Errorf("ssh-keygen not found in PATH, needed to look %s up in %s: %w", node.Host, node.KnownHostsFile, err)
	}
	out, err := exec.Command(keygen, "-F", node.Host, "-f", node.KnownHostsFile).Output()
	var exit *exec.ExitError
	switch {
	case err == nil && len(out) > 0:
		return nil
	case err == nil || errors.As(err, &exit) && exit.ExitCode() == 1:
		return unpinned(node, fmt.Errorf("it has no entry for %s", node.Host))
	default:
		return fmt.Errorf("look up %s in %s: %w", node.Host, node.KnownHostsFile, err)
	}
}

// unpinned is the refusal for a host whose key was never pinned.
func unpinned(node *inspector.Node, why error) error {
	return clierr.Failure("Host key verification failed: %s is not pinned (%v). orama ssh never trusts a host on first use; "+
		"pin its key with 'orama node setup --ip %s ...', which writes it to %s",
		node.Host, why, node.Host, node.KnownHostsFile)
}

const (
	// pinnedHostsDir and pinnedHostsFile are ~/.orama/known_hosts, the pin
	// 'orama node setup' keeps.
	pinnedHostsDir  = ".orama"
	pinnedHostsFile = "known_hosts"
)
