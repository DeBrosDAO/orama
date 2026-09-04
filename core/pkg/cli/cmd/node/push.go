package node

import (
	"github.com/DeBrosOfficial/network/pkg/cli/production/push"
	"github.com/spf13/cobra"
)

var pushFlags push.Flags

var pushCmd = &cobra.Command{
	Use:   "push",
	Short: "Push binary archive to remote nodes",
	Long: `Upload a pre-built binary archive to remote nodes.

By default, uses fanout distribution: uploads to one hub node,
then distributes to all others via server-to-server SCP.

Examples:
  orama node push --env devnet          # Fanout to all devnet nodes
  orama node push --env testnet --node 1.2.3.4  # Single node
  orama node push --env testnet --direct # Sequential upload to each node`,
	RunE: func(cmd *cobra.Command, args []string) error {
		return push.Run(&pushFlags)
	},
}

func init() {
	f := pushCmd.Flags()
	f.StringVar(&pushFlags.Env, "env", "", "Target environment (devnet, testnet) [required]")
	f.StringVar(&pushFlags.Node, "node", "", "Push to a single node IP only")
	f.BoolVar(&pushFlags.Direct, "direct", false, "Upload directly to each node (no hub fanout)")
}
