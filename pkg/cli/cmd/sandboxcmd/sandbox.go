package sandboxcmd

import (
	"fmt"
	"os"

	"github.com/DeBrosOfficial/network/pkg/cli/sandbox"
	"github.com/spf13/cobra"
)

// Cmd is the root command for sandbox operations.
var Cmd = &cobra.Command{
	Use:   "sandbox",
	Short: "Manage ephemeral Hetzner Cloud clusters for testing",
	Long: `Spin up temporary 5-node Orama clusters on Hetzner Cloud for development and testing.

Setup (one-time):
  orama sandbox setup

Usage:
  orama sandbox create [--name <name>]     Create a new 5-node cluster
  orama sandbox destroy [--name <name>]    Tear down a cluster
  orama sandbox list                       List active sandboxes
  orama sandbox status [--name <name>]     Show cluster health
  orama sandbox rollout [--name <name>]    Build + push + rolling upgrade
  orama sandbox ssh <node-number>          SSH into a sandbox node (1-5)
  orama sandbox reset                      Delete all infra and config to start fresh`,
}

var setupCmd = &cobra.Command{
	Use:   "setup",
	Short: "Interactive setup: Hetzner API key, domain, floating IPs, SSH key",
	RunE: func(cmd *cobra.Command, args []string) error {
		return sandbox.Setup()
	},
}

var createCmd = &cobra.Command{
	Use:   "create",
	Short: "Create a new 5-node sandbox cluster (~5 min)",
	RunE: func(cmd *cobra.Command, args []string) error {
		name, _ := cmd.Flags().GetString("name")
		return sandbox.Create(name)
	},
}

var destroyCmd = &cobra.Command{
	Use:   "destroy",
	Short: "Destroy a sandbox cluster and release resources",
	RunE: func(cmd *cobra.Command, args []string) error {
		name, _ := cmd.Flags().GetString("name")
		force, _ := cmd.Flags().GetBool("force")
		return sandbox.Destroy(name, force)
	},
}

var listCmd = &cobra.Command{
	Use:   "list",
	Short: "List active sandbox clusters",
	RunE: func(cmd *cobra.Command, args []string) error {
		return sandbox.List()
	},
}

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show cluster health report",
	RunE: func(cmd *cobra.Command, args []string) error {
		name, _ := cmd.Flags().GetString("name")
		return sandbox.Status(name)
	},
}

var rolloutCmd = &cobra.Command{
	Use:   "rollout",
	Short: "Build + push + rolling upgrade to sandbox cluster",
	RunE: func(cmd *cobra.Command, args []string) error {
		name, _ := cmd.Flags().GetString("name")
		return sandbox.Rollout(name)
	},
}

var resetCmd = &cobra.Command{
	Use:   "reset",
	Short: "Delete all sandbox infrastructure and config to start fresh",
	Long: `Deletes floating IPs, firewall, and SSH key from Hetzner Cloud,
then removes the local config (~/.orama/sandbox.yaml) and SSH keys.

Use this when you need to switch datacenter locations (floating IPs are
location-bound) or to completely start over with sandbox setup.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		return sandbox.Reset()
	},
}

var sshCmd = &cobra.Command{
	Use:   "ssh <node-number>",
	Short: "SSH into a sandbox node (1-5)",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name, _ := cmd.Flags().GetString("name")
		var nodeNum int
		if _, err := fmt.Sscanf(args[0], "%d", &nodeNum); err != nil {
			fmt.Fprintf(os.Stderr, "Invalid node number: %s (expected 1-5)\n", args[0])
			os.Exit(1)
		}
		return sandbox.SSHInto(name, nodeNum)
	},
}

func init() {
	// create flags
	createCmd.Flags().String("name", "", "Sandbox name (random if not specified)")

	// destroy flags
	destroyCmd.Flags().String("name", "", "Sandbox name (uses active if not specified)")
	destroyCmd.Flags().Bool("force", false, "Skip confirmation")

	// status flags
	statusCmd.Flags().String("name", "", "Sandbox name (uses active if not specified)")

	// rollout flags
	rolloutCmd.Flags().String("name", "", "Sandbox name (uses active if not specified)")

	// ssh flags
	sshCmd.Flags().String("name", "", "Sandbox name (uses active if not specified)")

	Cmd.AddCommand(setupCmd)
	Cmd.AddCommand(createCmd)
	Cmd.AddCommand(destroyCmd)
	Cmd.AddCommand(listCmd)
	Cmd.AddCommand(statusCmd)
	Cmd.AddCommand(rolloutCmd)
	Cmd.AddCommand(sshCmd)
	Cmd.AddCommand(resetCmd)
}
