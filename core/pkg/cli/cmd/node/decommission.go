package node

import (
	"github.com/DeBrosOfficial/network/pkg/cli/production/decommission"
	"github.com/spf13/cobra"
)

var decommissionCmd = &cobra.Command{
	Use:   "decommission",
	Short: "Remove one node from the cluster, then erase it",
	Long: `Retire a node from every store the cluster keeps, then wipe it.

Runs the cluster-side removal from a SURVIVOR: takes the node out of the raft
configuration (refusing if that would cost the cluster its quorum), writes an
eviction tombstone so nothing re-adds it automatically, and deletes its
wireguard_peers and dns_nodes rows. Then wipes the target, unless --offline.

Use --offline when the machine is already gone. The cluster-side removal still
happens; nothing is attempted against the target.

This is a DESTRUCTIVE operation. Use --force to skip confirmation.

Examples:
  orama node decommission --env testnet --node 1.2.3.4
  orama node decommission --env testnet --node 1.2.3.4 --offline   # VPS already deleted
  orama node decommission --env testnet --node 1.2.3.4 --force`,
	Run: func(cmd *cobra.Command, args []string) {
		decommission.Handle(args)
	},
	DisableFlagParsing: true,
}

var wipeCmd = &cobra.Command{
	Use:   "wipe",
	Short: "Erase Orama from remote nodes (target-side only)",
	Long: `Remove all Orama data, services and configuration from remote nodes.
Anyone relay keys at /var/lib/anon/ are preserved.

Target-side only: this says nothing to the cluster. If the node is still a
member, use 'orama node decommission' instead — otherwise the survivors keep
counting it toward quorum and re-adding its WireGuard peer.

This is a DESTRUCTIVE operation. Use --force to skip confirmation.

Examples:
  orama node wipe --env testnet                      # Wipe every node
  orama node wipe --env testnet --node 1.2.3.4       # Wipe one node
  orama node wipe --env testnet --nuclear             # Also remove shared binaries`,
	Run: func(cmd *cobra.Command, args []string) {
		decommission.HandleWipe(args)
	},
	DisableFlagParsing: true,
}
