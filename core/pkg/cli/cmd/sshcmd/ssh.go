package sshcmd

import (
	"fmt"
	"os"
	"os/exec"

	"github.com/DeBrosOfficial/network/pkg/cli"
	"github.com/DeBrosOfficial/network/pkg/cli/noderesolver"
	"github.com/DeBrosOfficial/network/pkg/cli/remotessh"
	"github.com/DeBrosOfficial/network/pkg/inspector"
	"github.com/spf13/cobra"
)

var envFlag string

// Cmd is the top-level "ssh" command — SSH into any node by IP or hostname.
var Cmd = &cobra.Command{
	Use:   "ssh <ip-or-hostname> [-- command]",
	Short: "SSH into a node",
	Long: `SSH into a node by IP address or hostname.
Resolves the SSH key from rootwallet automatically.

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
	nodes := []inspector.Node{node}
	cleanup, err := remotessh.PrepareNodeKeys(nodes)
	if err != nil {
		return fmt.Errorf("failed to resolve SSH key: %w", err)
	}
	defer cleanup()

	keyPath := nodes[0].SSHKey

	sshBin, err := exec.LookPath("ssh")
	if err != nil {
		return fmt.Errorf("ssh not found in PATH: %w", err)
	}

	sshArgs := []string{
		"-i", keyPath,
		"-o", "StrictHostKeyChecking=accept-new",
		"-o", "IdentitiesOnly=yes",
		fmt.Sprintf("%s@%s", node.User, node.Host),
	}
	if remoteCmd != "" {
		sshArgs = append(sshArgs, remoteCmd)
	}

	sshCmd := exec.Command(sshBin, sshArgs...)
	sshCmd.Stdin = os.Stdin
	sshCmd.Stdout = os.Stdout
	sshCmd.Stderr = os.Stderr
	return sshCmd.Run()
}
