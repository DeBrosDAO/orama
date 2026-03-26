package node

import (
	"github.com/DeBrosOfficial/network/pkg/cli/production/push"
	"github.com/spf13/cobra"
)

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
	Run: func(cmd *cobra.Command, args []string) {
		push.Handle(args)
	},
	DisableFlagParsing: true,
}
