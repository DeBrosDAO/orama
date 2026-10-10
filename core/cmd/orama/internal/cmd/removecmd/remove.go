// Package removecmd is `orama remove`: take one node out of your network, safely.
package removecmd

import (
	"github.com/spf13/cobra"

	"github.com/DeBrosOfficial/network/cmd/orama/internal/production/removenode"
)

// Cmd is the top-level "remove" command.
var Cmd = New()

// New builds the command; each call has its own flag storage.
func New() *cobra.Command {
	var opts removenode.Options
	cmd := &cobra.Command{
		Use:   "remove",
		Short: "Remove one node from your network, then erase it",
		Long: `Take one node out of every store the network keeps, then wipe it.

Before anything changes, remove prints what the removal costs every raft cluster
the node is a voter in (the platform cluster and each namespace it serves) and
refuses if any of them would lose quorum. It also refuses:

  a node in the validator set       erasing it destroys the validator's consensus key
                                    and jails the validator; move the key first, or
                                    pass --drop-validator
  a node with the global layer      it may be registered on the chain with a bond; say
                                    what happens to that: --chain-node-id <id> retires
                                    it, --no-chain leaves it
  a node that holds storage deals   the chain refuses to retire a node whose deals
                                    still reserve bytes

With --chain-node-id the node is retired on the chain first (MsgRetireNode, signed
by your RootWallet, sent through a surviving node's chain over SSH): its service keys
are revoked and its bonds start to unbond. If the chain or the RootWallet refuses,
nothing has been removed. Then the node leaves the raft configuration, an eviction
tombstone keeps anything from re-adding it, its mesh address, nameserver slot,
namespace memberships, port blocks and TURN and SFU allocations are released, its DNS
records are purged, and the machine is wiped.

Use --offline when the machine is already gone: the removal is done from the
survivors and nothing is attempted on the target. It cannot be asked whether it
was registered on the chain, so its registration is left alone and the plan says
so; --chain-node-id retires it through a surviving node. Every step is keyed on the node and
safe to repeat, so a removal that failed part way is finished by running it again.

--dry-run prints the quorum arithmetic and every step, changing nothing. This is
DESTRUCTIVE: it asks you to type 'yes' unless --yes is given.

Examples:
  orama remove --node 203.0.113.9 --dry-run
  orama remove --node 203.0.113.9
  orama remove --node 203.0.113.9 --chain-node-id node-9
  orama remove --node 203.0.113.9 --offline`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return removenode.Run(cmd.Context(), opts)
		},
	}
	f := cmd.Flags()
	f.StringVar(&opts.Env, "env", "", "Network the node belongs to (default: the active one)")
	f.StringVar(&opts.Node, "node", "", "Public IP of the node to remove [required]")
	f.BoolVar(&opts.Offline, "offline", false, "The machine is already gone: retire it from the cluster only, do not wipe it")
	f.BoolVar(&opts.Nuclear, "nuclear", false, "When wiping, also remove the shared binaries, the Tor package and the system accounts Orama created")
	f.BoolVar(&opts.Yes, "yes", false, "Do not ask for confirmation (DESTRUCTIVE)")
	f.BoolVar(&opts.DryRun, "dry-run", false, "Print the quorum impact and every step, change nothing")
	f.StringVar(&opts.ChainNodeID, "chain-node-id", "", "The node's id in the chain's node registry: retire it there before removing it")
	f.StringVar(&opts.ChainID, "chain-id", "", "The chain id you expect, for a network that is on no registry network (one from the registry already names it); the wallet signs for no other chain")
	f.BoolVar(&opts.NoChain, "no-chain", false, "Leave the node's chain registration alone (its bonds stay locked until you retire it)")
	f.BoolVar(&opts.DropValidator, "drop-validator", false, "Remove the node although it signs for the validator set; its consensus key is erased with it")
	return cmd
}
