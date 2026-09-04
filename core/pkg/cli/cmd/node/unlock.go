package node

import (
	"github.com/DeBrosOfficial/network/pkg/cli/production/unlock"
	"github.com/spf13/cobra"
)

var unlockFlags unlock.Flags

var unlockCmd = &cobra.Command{
	Use:   "unlock",
	Short: "Unlock an OramaOS genesis node",
	Long: `Manually unlock a genesis OramaOS node that cannot reconstruct its LUKS key
via Shamir shares (not enough peers online).

This is only needed for the genesis node before enough peers have joined for
Shamir-based unlock. Once 5+ peers exist, the genesis node transitions to
normal Shamir unlock and this command is no longer needed.

The encrypted genesis key is written where the node was created, and the
OramaOS agent does not serve it, so --key-file is required. The command used to
try fetching it from the node first, on a path the agent has never served, and
spent ten seconds timing out before telling you to pass the flag.

Usage:
  orama node unlock --genesis --node-ip <wg-ip> --key-file <path>

The node must be reachable over WireGuard on port 9998.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		return unlock.Run(&unlockFlags)
	},
}

func init() {
	unlockCmd.Flags().StringVar(&unlockFlags.NodeIP, "node-ip", "", "WireGuard IP of the OramaOS node (required)")
	unlockCmd.Flags().BoolVar(&unlockFlags.Genesis, "genesis", false, "Confirm genesis node unlock")
	unlockCmd.Flags().StringVar(&unlockFlags.KeyFile, "key-file", "",
		"Path to the encrypted genesis key file (required)")
	_ = unlockCmd.MarkFlagRequired("key-file")
}
