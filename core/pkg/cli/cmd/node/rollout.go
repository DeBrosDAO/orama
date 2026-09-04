package node

import (
	"github.com/DeBrosOfficial/network/pkg/cli/production/rollout"
	rolloutplan "github.com/DeBrosOfficial/network/pkg/rollout"
	"github.com/spf13/cobra"
)

var rolloutFlags rollout.Flags

var rolloutCmd = &cobra.Command{
	Use:   "rollout",
	Short: "Build, push, and rolling upgrade all nodes in an environment",
	Long: `Full deployment pipeline: build binary archive locally, push to all nodes,
then perform a rolling upgrade (one node at a time).

Examples:
  orama node rollout --env testnet             # Full: build + push + rolling upgrade
  orama node rollout --env testnet --no-build  # Skip build, use existing archive
  orama node rollout --env testnet --yes       # Skip confirmation`,
	RunE: func(cmd *cobra.Command, args []string) error {
		return rollout.Run(&rolloutFlags)
	},
}

func init() {
	f := rolloutCmd.Flags()
	f.StringVar(&rolloutFlags.Env, "env", "", "Target environment (devnet, testnet) [required]")
	f.BoolVar(&rolloutFlags.NoBuild, "no-build", false, "Skip build step (use existing archive)")
	f.BoolVar(&rolloutFlags.Yes, "yes", false, "Skip confirmation")
	f.IntVar(&rolloutFlags.Delay, "delay", int(rolloutplan.GateBudget.Seconds()),
		"Seconds a node has to rejoin the cluster after its upgrade before the rollout stops")
}
