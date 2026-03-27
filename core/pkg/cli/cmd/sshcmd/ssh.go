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
	Use:   "ssh <ip-or-hostname>",
	Short: "SSH into a node",
	Long: `SSH into a node by IP address or hostname.
Resolves the SSH key from rootwallet automatically.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		target := args[0]

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
				return sshInto(n)
			}
		}

		// Not found — try direct SSH with default vault target
		fmt.Printf("Node %q not found in %s nodes, attempting direct SSH...\n", target, env)
		return sshInto(inspector.Node{
			Host:        target,
			User:        "root",
			VaultTarget: target + "/root",
		})
	},
}

func init() {
	Cmd.Flags().StringVar(&envFlag, "env", "", "Environment to search (default: active)")
}

func sshInto(node inspector.Node) error {
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

	sshCmd := exec.Command(sshBin,
		"-i", keyPath,
		"-o", "StrictHostKeyChecking=accept-new",
		fmt.Sprintf("%s@%s", node.User, node.Host),
	)
	sshCmd.Stdin = os.Stdin
	sshCmd.Stdout = os.Stdout
	sshCmd.Stderr = os.Stderr
	return sshCmd.Run()
}
