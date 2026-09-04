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

One node's data is kept. Every other node's raft log and database are DELETED
and rebuilt from it. Nothing is backed up: there is no copy to restore from
afterwards, and the deleted nodes' data is gone. Take a backup yourself first
if the surviving node might not be the right one.

What happens:
  1. Stop orama-node on every node
  2. Reset the kept node to a single-member cluster, preserving its data
  3. Start it and confirm it comes back as Leader with its data intact
  4. Delete raft.db, raft/, db.sqlite (+shm/wal) and rsnapshots on every other
     node
  5. Start them one at a time; each pulls a full snapshot from the kept node
  6. Verify cluster health

Which node is kept decides which copy of the data survives. Without --leader
the command reads every node's applied index, keeps the furthest ahead, and
prints what each one reported before asking you to confirm. --leader overrides
that.

Use --leader-raft-addr when quorum is already lost and rqlite is not answering
anywhere, so the leader's raft address cannot be read from the cluster.

This is a DESTRUCTIVE operation. Use --force to skip confirmation.

Examples:
  orama node recover-raft --env testnet
  orama node recover-raft --env testnet --leader 1.2.3.4
  orama node recover-raft --env devnet --leader-raft-addr 10.0.0.1:10101 --force`,
	RunE: func(cmd *cobra.Command, args []string) error {
		return recover.Run(&recoverFlags)
	},
}

func init() {
	f := recoverRaftCmd.Flags()
	f.StringVar(&recoverFlags.Env, "env", "", "Target environment (devnet, testnet) [required]")
	f.StringVar(&recoverFlags.Leader, "leader", "",
		"IP of the node whose data to keep; default is the node with the highest applied index")
	f.StringVar(&recoverFlags.LeaderRaftAddr, "leader-raft-addr", "", "Explicit leader raft address host:port (e.g. 10.0.0.1:10101). Use when quorum is already lost so the leader can't be auto-resolved; bypasses the live-Leader check.")
	f.BoolVar(&recoverFlags.Force, "force", false, "Skip confirmation (DESTRUCTIVE)")
}
