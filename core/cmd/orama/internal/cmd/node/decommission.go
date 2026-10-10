package node

import (
	"fmt"

	"github.com/DeBrosOfficial/network/cmd/orama/internal/clierr"
	"github.com/DeBrosOfficial/network/cmd/orama/internal/cmdmeta"
	"github.com/DeBrosOfficial/network/cmd/orama/internal/production/decommission"
	"github.com/DeBrosOfficial/network/cmd/orama/internal/production/removenode"
	"github.com/spf13/cobra"
)

var (
	// removeFlags are the flags of `orama remove`, which this path runs.
	removeFlags removenode.Options
	// removeForce is --force, the old name of --yes.
	removeForce bool
	wipeFlags   decommission.WipeFlags
)

// removeReplacedNotice is what the old path says: it runs `orama remove`, which
// adds the node's chain registration and the refusals a newcomer needs.
const removeReplacedNotice = "Note: `orama node remove` is replaced by `orama remove` and runs it: it also retires the node on the chain (--chain-node-id) and refuses to erase a validator."

var decommissionCmd = &cobra.Command{
	Use:    "remove",
	Short:  "Remove one node from the cluster, then erase it (replaced by orama remove)",
	Hidden: true,
	Long: `The old path of 'orama remove', which is the command to use: this one runs it
(--force is --yes) and prints a notice. The removal is the same: the quorum
arithmetic for every raft cluster the node votes in, the tombstone, the
retirement and the wipe, plus the chain: a node in the validator set is refused
unless --drop-validator, and a node with the global layer needs --chain-node-id
or --no-chain. See 'orama remove --help'.

Examples:
  orama node remove --env testnet --node 1.2.3.4 --dry-run   # Show the plan only
  orama node remove --env testnet --node 1.2.3.4
  orama node remove --env testnet --node 1.2.3.4 --offline   # VPS already deleted
  orama node remove --env testnet --node 1.2.3.4 --force`,
	RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Fprintln(cmd.ErrOrStderr(), removeReplacedNotice)
		if removeFlags.Env == "" {
			return clierr.Usage("--env is required\nUsage: orama node remove --env <devnet|testnet> --node <ip> [--offline] [--force]")
		}
		removeFlags.Yes = removeForce
		return removenode.Run(cmd.Context(), removeFlags)
	},
}

var wipeCmd = &cobra.Command{
	Use:   "wipe",
	Short: "Erase Orama from remote nodes (target-side only)",
	Long: `Remove all Orama data, services and configuration from remote nodes.
Tor is left installed (its config and state are removed); --nuclear purges it.
The wipe ends by listing what of Orama is still on the machine (LEFTOVER lines) and fails if
anything is; the system accounts are removed only with --nuclear.

Target-side only: this says nothing to the cluster. If the node is still a
member, use 'orama remove' instead — otherwise the survivors keep
counting it toward quorum and re-adding its WireGuard peer.

This is a DESTRUCTIVE operation. Use --force to skip confirmation.

Examples:
  orama node wipe --env testnet                      # Wipe every node
  orama node wipe --env testnet --node 1.2.3.4       # Wipe one node
  orama node wipe --env testnet --nuclear             # Also remove shared binaries and accounts`,
	RunE: func(cmd *cobra.Command, args []string) error {
		return decommission.RunWipe(&wipeFlags)
	},
}

func init() {
	cmdmeta.MarkListed(decommissionCmd)
	d := decommissionCmd.Flags()
	d.StringVar(&removeFlags.Env, "env", "", "Target environment (devnet, testnet) [required]")
	d.StringVar(&removeFlags.Node, "node", "", "Public IP of the node to remove [required]")
	d.BoolVar(&removeFlags.Offline, "offline", false, "The node is already gone: retire it cluster-side only, do not try to wipe it")
	d.BoolVar(&removeFlags.Nuclear, "nuclear", false, "When wiping, also remove shared binaries, the Tor package and the system accounts Orama created")
	d.BoolVar(&removeForce, "force", false, "Skip confirmation (DESTRUCTIVE)")
	d.BoolVar(&removeFlags.DryRun, "dry-run", false, "Print the quorum impact and the statements, change nothing")
	d.StringVar(&removeFlags.ChainNodeID, "chain-node-id", "", "The node's id in the chain's node registry: retire it there before removing it")
	d.StringVar(&removeFlags.ChainID, "chain-id", "", "The chain id you expect, for a network that is on no registry network (one from the registry already names it); the wallet signs for no other chain")
	d.BoolVar(&removeFlags.NoChain, "no-chain", false, "Leave the node's chain registration alone (its bonds stay locked until you retire it)")
	d.BoolVar(&removeFlags.DropValidator, "drop-validator", false, "Remove the node although it signs for the validator set; its consensus key is erased with it")

	w := wipeCmd.Flags()
	w.StringVar(&wipeFlags.Env, "env", "", "Target environment (devnet, testnet) [required]")
	w.StringVar(&wipeFlags.Node, "node", "", "Public IP of the node to wipe; omit to wipe every node in the environment")
	w.BoolVar(&wipeFlags.Nuclear, "nuclear", false, "Also remove shared binaries (rqlited, ipfs, caddy, ...), the Tor package and the system accounts Orama created (orama, orama-*, ntfy)")
	w.BoolVar(&wipeFlags.Force, "force", false, "Skip confirmation (DESTRUCTIVE)")
}
