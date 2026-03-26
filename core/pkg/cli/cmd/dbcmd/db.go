package dbcmd

import (
	"github.com/DeBrosOfficial/network/pkg/cli/db"
	"github.com/spf13/cobra"
)

// Cmd is the root command for database operations.
var Cmd = &cobra.Command{
	Use:   "db",
	Short: "Manage SQLite databases",
	Long:  `Create and manage per-namespace SQLite databases.`,
}

func init() {
	Cmd.AddCommand(db.CreateCmd)
	Cmd.AddCommand(db.QueryCmd)
	Cmd.AddCommand(db.ListCmd)
	Cmd.AddCommand(db.BackupCmd)
	Cmd.AddCommand(db.BackupsCmd)
}
