package node

import (
	"github.com/DeBrosOfficial/network/pkg/cli/production/upgrade"
	"github.com/spf13/cobra"
)

var upgradeCmd = &cobra.Command{
	Use:   "upgrade",
	Short: "Upgrade existing installation (requires sudo)",
	Long: `Upgrade the Orama node binary and optionally restart services.
Uses rolling restart with quorum safety to ensure zero downtime.`,
	Run: func(cmd *cobra.Command, args []string) {
		upgrade.Handle(args)
	},
	DisableFlagParsing: true,
}
