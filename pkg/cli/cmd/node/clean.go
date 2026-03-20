package node

import (
	"github.com/DeBrosOfficial/network/pkg/cli/production/clean"
	"github.com/spf13/cobra"
)

var cleanCmd = &cobra.Command{
	Use:   "clean",
	Short: "Clean (wipe) remote nodes for reinstallation",
	Long: `Remove all Orama data, services, and configuration from remote nodes.
Anyone relay keys at /var/lib/anon/ are preserved.

This is a DESTRUCTIVE operation. Use --force to skip confirmation.

Examples:
  orama node clean --env testnet                       # Clean all testnet nodes
  orama node clean --env testnet --node 1.2.3.4        # Clean specific node
  orama node clean --env testnet --nuclear              # Also remove shared binaries
  orama node clean --env testnet --force                # Skip confirmation`,
	Run: func(cmd *cobra.Command, args []string) {
		clean.Handle(args)
	},
	DisableFlagParsing: true,
}
