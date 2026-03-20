package node

import (
	"github.com/DeBrosOfficial/network/pkg/cli/production/rollout"
	"github.com/spf13/cobra"
)

var rolloutCmd = &cobra.Command{
	Use:   "rollout",
	Short: "Build, push, and rolling upgrade all nodes in an environment",
	Long: `Full deployment pipeline: build binary archive locally, push to all nodes,
then perform a rolling upgrade (one node at a time).

Examples:
  orama node rollout --env testnet             # Full: build + push + rolling upgrade
  orama node rollout --env testnet --no-build  # Skip build, use existing archive
  orama node rollout --env testnet --yes       # Skip confirmation`,
	Run: func(cmd *cobra.Command, args []string) {
		rollout.Handle(args)
	},
	DisableFlagParsing: true,
}
