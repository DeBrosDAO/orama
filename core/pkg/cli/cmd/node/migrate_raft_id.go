package node

import (
	"github.com/DeBrosOfficial/network/pkg/cli/production/raftid"
	"github.com/spf13/cobra"
)

var raftIDFlags raftid.Flags

var migrateRaftIDCmd = &cobra.Command{
	Use:   "migrate-raft-id",
	Short: "Move nodes to stable, peer-id-based raft identities (one-time)",
	Long: `Give each node a raft identity that survives an address change.

RQLite defaults a node's raft id to its raft advertise address, so identity has
been a function of routing: give the same machine a new overlay address — a
replacement, a WireGuard re-provision, a 10.0.0.x reassignment — and it mints a
new raft id, joins as a SECOND member, and the old entry stays in the
configuration as a voter nothing can reach. Two such events on a five-voter
cluster leave quorum at 3-of-7 with five live voters; one more failure freezes
the registry.

RQLite cannot rename a member in place, so this is a deliberate migration rather
than something an upgrade does silently. Nodes are migrated ONE AT A TIME. For
each: the quorum arithmetic is checked, the old id is removed from the raft
configuration and tombstoned, the node's local raft state is discarded, and it
rejoins under its libp2p peer id and replicates back from the leader. The next
node is not touched until the previous one is back in the configuration.

Safe to re-run: nodes already on a stable id are skipped, so an interrupted run
continues where it stopped.

Examples:
  orama node migrate-raft-id --env testnet --dry-run
  orama node migrate-raft-id --env testnet
  orama node migrate-raft-id --env testnet --node 1.2.3.4`,
	RunE: func(cmd *cobra.Command, args []string) error {
		return raftid.Run(&raftIDFlags)
	},
}

func init() {
	f := migrateRaftIDCmd.Flags()
	f.StringVar(&raftIDFlags.Env, "env", "", "Target environment [required]")
	f.StringVar(&raftIDFlags.Node, "node", "", "Migrate only this public IP. Default: every node that needs it")
	f.BoolVar(&raftIDFlags.DryRun, "dry-run", false, "Report what would change and exit")
	f.BoolVar(&raftIDFlags.Force, "force", false, "Skip the confirmation prompt")
}
