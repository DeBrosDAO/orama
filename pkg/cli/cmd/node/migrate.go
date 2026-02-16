package node

import (
	"github.com/DeBrosOfficial/network/pkg/cli/production/migrate"
	"github.com/spf13/cobra"
)

var migrateCmd = &cobra.Command{
	Use:   "migrate",
	Short: "Migrate from old unified setup (requires sudo)",
	Run: func(cmd *cobra.Command, args []string) {
		migrate.Handle(args)
	},
	DisableFlagParsing: true,
}
