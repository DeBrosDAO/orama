package node

import (
	"github.com/DeBrosOfficial/network/pkg/cli/production/decommission"
	"github.com/spf13/cobra"
)

var cleanFlags decommission.WipeFlags

var cleanCmd = &cobra.Command{
	Use:        "clean",
	Short:      "Deprecated: use 'orama node wipe' or 'orama node decommission'",
	Deprecated: "use 'orama node wipe' to erase a node, or 'orama node decommission' to remove one from the cluster and erase it.",
	Long: `DEPRECATED. Use 'orama node wipe' or 'orama node decommission'.

'clean' only ever erased the target. It said nothing to the rest of the cluster,
so a cleaned node stayed a configured raft voter counted toward quorum, kept its
wireguard_peers row re-applied to every survivor's interface, and kept its
dns_nodes row. It also stopped only the legacy host unit names, leaving tenant
'orama-namespace-*@*' units running under a deleted data directory.

  orama node wipe           erases a node (what clean did, fixed)
  orama node decommission   removes one node from the cluster, then erases it

This command now runs 'wipe'.

Examples:
  orama node wipe --env testnet --node 1.2.3.4
  orama node decommission --env testnet --node 1.2.3.4`,
	RunE: func(cmd *cobra.Command, args []string) error {
		return decommission.RunWipe(&cleanFlags)
	},
}

func init() {
	f := cleanCmd.Flags()
	f.StringVar(&cleanFlags.Env, "env", "", "Target environment (devnet, testnet) [required]")
	f.StringVar(&cleanFlags.Node, "node", "", "Public IP of the node to wipe; omit to wipe every node in the environment")
	f.BoolVar(&cleanFlags.Nuclear, "nuclear", false, "Also remove shared binaries (rqlited, ipfs, caddy, ...)")
	f.BoolVar(&cleanFlags.Force, "force", false, "Skip confirmation (DESTRUCTIVE)")
}
