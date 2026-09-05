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
	Use:     "remove",
	Aliases: []string{"decommission"},
	Short:   "Remove one node from the cluster, then erase it",
	Long: `Retire a node from every store the cluster keeps, then wipe it.

Runs the cluster-side removal from a SURVIVOR. First it prints what the removal
costs every raft cluster the node is a voter in — the platform cluster and each
namespace it serves — and refuses if any of them would lose quorum. Then it
takes the node out of the raft configuration, writes an eviction tombstone so
nothing re-adds it automatically, releases its mesh address, nameserver slot,
namespace memberships, namespace port blocks and its TURN and SFU allocations,
and marks it retired so the cluster purges its DNS records. Then it wipes the
target, unless --offline.

Use --offline when the machine is already gone. The cluster-side removal still
happens; nothing is attempted against the target.

Every step is keyed on the node and safe to repeat, so a removal that failed
part way through is finished by running it again.

This is a DESTRUCTIVE operation. Use --force to skip confirmation.

Examples:
  orama node remove --env testnet --node 1.2.3.4 --dry-run   # Show the plan only
  orama node remove --env testnet --node 1.2.3.4
  orama node remove --env testnet --node 1.2.3.4 --offline   # VPS already deleted
  orama node remove --env testnet --node 1.2.3.4 --force`,
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
	d.BoolVar(&decommissionFlags.DryRun, "dry-run", false, "Print the quorum impact and the statements, change nothing")

	w := wipeCmd.Flags()
	w.StringVar(&wipeFlags.Env, "env", "", "Target environment (devnet, testnet) [required]")
	w.StringVar(&wipeFlags.Node, "node", "", "Public IP of the node to wipe; omit to wipe every node in the environment")
	w.BoolVar(&wipeFlags.Nuclear, "nuclear", false, "Also remove shared binaries (rqlited, ipfs, caddy, ...)")
	w.BoolVar(&wipeFlags.Force, "force", false, "Skip confirmation (DESTRUCTIVE)")
}
