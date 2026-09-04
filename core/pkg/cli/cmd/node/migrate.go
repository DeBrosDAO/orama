package node

import (
	"github.com/DeBrosOfficial/network/pkg/cli/production/migrate"
	"github.com/spf13/cobra"
)

var migrateOpts migrate.Options

var migrateCmd = &cobra.Command{
	Use:   "migrate",
	Short: "Migrate from old unified setup (requires sudo)",
	Run: func(cmd *cobra.Command, args []string) {
		migrate.Run(migrateOpts)
	},
}

func init() {
	migrateCmd.Flags().BoolVar(&migrateOpts.DryRun, "dry-run", false, "Show what would be migrated without making changes")
}
