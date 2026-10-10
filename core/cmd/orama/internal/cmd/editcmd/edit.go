// Package editcmd is `orama edit`: change a node you already installed.
package editcmd

import (
	"os"

	"github.com/mattn/go-isatty"
	"github.com/spf13/cobra"

	"github.com/DeBrosOfficial/network/cmd/orama/internal/production/nodeedit"
)

// Flag names that say whether a setting was given at all.
const (
	storageFlag = "storage-gb"
	exitFlag    = "exit"
	globalFlag  = "global"
)

// Cmd is the top-level "edit" command.
var Cmd = New()

// New builds the command; each call has its own flag storage.
func New() *cobra.Command {
	var (
		opts      nodeedit.Options
		storageGB uint64
		exit      bool
		global    bool
	)
	cmd := &cobra.Command{
		Use:   "edit",
		Short: "Change a node you already installed: storage size, exit role",
		Long: `Change a setting of a node that is already installed, without installing it again.
With no setting flag, in a terminal, it opens a form: choose the node, then the
settings it has. With flags it changes exactly what they name.

  --storage-gb N    the public storage capacity the node offers. The node's public
                    Kubo is sized for N GB (its StorageMax becomes N plus 10%) and
                    restarted, and the capacity is declared on the chain
                    (MsgDeclareCapacity, signed by your RootWallet through an SSH
                    tunnel to a node's chain). The chain refuses a capacity the
                    node's role bond does not back, and one below the bytes deals
                    already reserve; if it refuses, the node is left as it was.
                    The chain needs the node's id there: --chain-node-id (see
                    'orama chain node <id>'), or --no-chain to resize the node only.
  --exit=true|false switch the node's Tor relay between a plain relay and an exit by
                    rewriting only the exit section of its torrc, and restart the
                    relay. An exit needs a network whose Tor file allows exits. The
                    node's roles on the chain are not changed by this.
  --global=...      the global layer cannot be turned on or off here, and edit says
                    what does it: running setup again for the IP adds it, 'orama remove'
                    takes a node out.

The change is made on the node by its own CLI, one node at a time, and the plan is
shown first; --yes skips the question. A node on a release without the node-side
command needs 'orama upgrade' first.

Examples:
  orama edit                                    # The form
  orama edit --node 203.0.113.7 --storage-gb 200 --chain-node-id node-7
  orama edit --node 203.0.113.7 --exit=true --yes`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if cmd.Flags().Changed(storageFlag) {
				opts.Settings.StorageGB = &storageGB
			}
			if cmd.Flags().Changed(exitFlag) {
				opts.Settings.Exit = &exit
			}
			if cmd.Flags().Changed(globalFlag) {
				opts.Settings.Global = &global
			}
			opts.Interactive = isatty.IsTerminal(os.Stdin.Fd()) && isatty.IsTerminal(os.Stdout.Fd())
			opts.Out = cmd.OutOrStdout()
			return nodeedit.Run(cmd.Context(), opts)
		},
	}
	f := cmd.Flags()
	f.StringVar(&opts.Env, "env", "", "Network the node belongs to (default: the active one)")
	f.StringVar(&opts.Node, "node", "", "Public IP of the node to edit (default: ask)")
	f.Uint64Var(&storageGB, storageFlag, 0, "Public storage capacity to offer, in GB")
	f.BoolVar(&exit, exitFlag, false, "Make the node's Tor relay an exit (true) or a plain relay (false)")
	f.BoolVar(&global, globalFlag, false, "Ask for the global layer on or off (refused, with what does it)")
	f.StringVar(&opts.ChainNodeID, "chain-node-id", "", "The node's id in the chain's node registry, to declare its capacity there")
	f.StringVar(&opts.ChainID, "chain-id", "", "The chain id you expect, for a network that is on no registry network (one from the registry already names it); the wallet signs for no other chain")
	f.BoolVar(&opts.NoChain, "no-chain", false, "Resize the node without declaring the capacity on the chain")
	f.BoolVar(&opts.Yes, "yes", false, "Do not ask for confirmation")
	return cmd
}
