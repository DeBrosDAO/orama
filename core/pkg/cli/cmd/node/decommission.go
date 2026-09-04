package node

import (
	"github.com/DeBrosOfficial/network/pkg/cli/production/decommission"
	"github.com/spf13/cobra"
)

var (
	decommissionFlags decommission.Flags
	wipeFlags         decommission.WipeFlags
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
	RunE: func(cmd *cobra.Command, args []string) error {
		return decommission.Run(&decommissionFlags)
	},
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
	RunE: func(cmd *cobra.Command, args []string) error {
		return decommission.RunWipe(&wipeFlags)
	},
}

func init() {
	d := decommissionCmd.Flags()
	d.StringVar(&decommissionFlags.Env, "env", "", "Target environment (devnet, testnet) [required]")
	d.StringVar(&decommissionFlags.Node, "node", "", "Public IP of the node to remove [required]")
	d.BoolVar(&decommissionFlags.Offline, "offline", false, "The node is already gone: retire it cluster-side only, do not try to wipe it")
	d.BoolVar(&decommissionFlags.Nuclear, "nuclear", false, "When wiping, also remove shared binaries")
	d.BoolVar(&decommissionFlags.Force, "force", false, "Skip confirmation (DESTRUCTIVE)")

	w := wipeCmd.Flags()
	w.StringVar(&wipeFlags.Env, "env", "", "Target environment (devnet, testnet) [required]")
	w.StringVar(&wipeFlags.Node, "node", "", "Public IP of the node to wipe; omit to wipe every node in the environment")
	w.BoolVar(&wipeFlags.Nuclear, "nuclear", false, "Also remove shared binaries (rqlited, ipfs, caddy, ...)")
	w.BoolVar(&wipeFlags.Force, "force", false, "Skip confirmation (DESTRUCTIVE)")
}
