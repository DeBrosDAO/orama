package node

import (
	"github.com/DeBrosOfficial/network/pkg/cli/production/recover"
	"github.com/spf13/cobra"
)

var recoverRaftCmd = &cobra.Command{
	Use:   "recover-raft",
	Short: "Recover RQLite cluster from split-brain",
	Long: `Recover the RQLite Raft cluster from split-brain failure.

Strategy:
  1. Stop orama-node on ALL nodes simultaneously
  2. Backup and delete raft/ on non-leader nodes
  3. Start leader node, wait for Leader state
  4. Start remaining nodes in batches
  5. Verify cluster health

The --leader flag must point to the node with the highest commit index.

This is a DESTRUCTIVE operation. Use --force to skip confirmation.

Examples:
  orama node recover-raft --env testnet --leader 1.2.3.4
  orama node recover-raft --env devnet --leader 1.2.3.4 --force`,
	Run: func(cmd *cobra.Command, args []string) {
		recover.Handle(args)
	},
	DisableFlagParsing: true,
}
