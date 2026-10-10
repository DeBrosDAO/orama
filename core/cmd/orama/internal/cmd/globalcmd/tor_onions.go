package globalcmd

import (
	"fmt"

	"github.com/DeBrosOfficial/network/cmd/orama/internal/clierr"
	"github.com/DeBrosOfficial/network/pkg/tornet"
	"github.com/spf13/cobra"
)

var onionsFlags struct{ networkFile string }

var onionsCmd = &cobra.Command{
	Use:   "onions",
	Short: "The validator onion services the network file lists",
}

var onionsAddCmd = &cobra.Command{
	Use:   "add ADDR.onion[:PORT]...",
	Short: "Add validator onion services to the network file clients join with",
	Long: `A validator's onion address exists only once its onion role has started, which
is after the ceremony wrote tor-network.json. Read it on the validator with
'orama global tor info' (as root), then add it to the network file here and
republish the file to clients: orama maint vpn, --onion-network and the relay reporter
all read validator_onions from it.

Each address is checked as a v3 onion address with an optional port (default 80,
the port the onion service answers on). An address already listed is not listed
twice. The file is replaced atomically and must already be a valid network file;
nothing is written when an address is refused. Relays and authorities ignore
validator_onions, so adding an onion never needs a node restart.`,
	Args: cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if onionsFlags.networkFile == "" {
			return clierr.Usage("--network-file is required: the tor-network.json to update")
		}
		n, added, err := tornet.AddValidatorOnionsToFile(onionsFlags.networkFile, args...)
		if err != nil {
			return clierr.Usage("%v", err)
		}
		fmt.Fprintf(cmd.OutOrStdout(), "%s lists %d validator onion services (%d added).\n", onionsFlags.networkFile, len(n.ValidatorOnions), added)
		return nil
	},
}

func init() {
	onionsAddCmd.Flags().StringVar(&onionsFlags.networkFile, "network-file", "", "The tor-network.json to update [required]")
	onionsCmd.AddCommand(onionsAddCmd)
	maintTorCmd.AddCommand(onionsCmd)
}
