package node

import (
	"github.com/DeBrosOfficial/network/pkg/cli/production/unlock"
	"github.com/spf13/cobra"
)

var unlockCmd = &cobra.Command{
	Use:   "unlock",
	Short: "Unlock an OramaOS genesis node",
	Long: `Manually unlock a genesis OramaOS node that cannot reconstruct its LUKS key
via Shamir shares (not enough peers online).

This is only needed for the genesis node before enough peers have joined for
Shamir-based unlock. Once 5+ peers exist, the genesis node transitions to
normal Shamir unlock and this command is no longer needed.

Usage:
  orama node unlock --genesis --node-ip <wg-ip>

The node must be reachable over WireGuard on port 9998.`,
	Run: func(cmd *cobra.Command, args []string) {
		unlock.Handle(args)
	},
	DisableFlagParsing: true,
}
