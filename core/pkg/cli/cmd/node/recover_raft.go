package node

import (
	"github.com/DeBrosOfficial/network/pkg/cli/production/recover"
	"github.com/spf13/cobra"
)

var recoverFlags recover.Flags

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
	RunE: func(cmd *cobra.Command, args []string) error {
		return recover.Run(&recoverFlags)
	},
}

func init() {
	f := recoverRaftCmd.Flags()
	f.StringVar(&recoverFlags.Env, "env", "", "Target environment (devnet, testnet) [required]")
	f.StringVar(&recoverFlags.Leader, "leader", "", "Leader node IP (node with highest commit index) [required]")
	f.StringVar(&recoverFlags.LeaderRaftAddr, "leader-raft-addr", "", "Explicit leader raft address host:port (e.g. 10.0.0.1:10101). Use when quorum is already lost so the leader can't be auto-resolved; bypasses the live-Leader check.")
	f.BoolVar(&recoverFlags.Force, "force", false, "Skip confirmation (DESTRUCTIVE)")
}
