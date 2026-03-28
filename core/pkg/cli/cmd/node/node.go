package node

import (
	"github.com/spf13/cobra"
)

// Cmd is the root command for node operator commands (was "prod").
var Cmd = &cobra.Command{
	Use:   "node",
	Short: "Node operator commands (requires sudo for most operations)",
	Long: `Manage the Orama node running on this machine.
Includes install, upgrade, start/stop/restart, status, logs, and more.
Most commands require root privileges (sudo).`,
}

func init() {
	Cmd.AddCommand(installCmd)
	Cmd.AddCommand(uninstallCmd)
	Cmd.AddCommand(upgradeCmd)
	Cmd.AddCommand(startCmd)
	Cmd.AddCommand(stopCmd)
	Cmd.AddCommand(restartCmd)
	Cmd.AddCommand(statusCmd)
	Cmd.AddCommand(logsCmd)
	Cmd.AddCommand(inviteCmd)
	Cmd.AddCommand(migrateCmd)
	Cmd.AddCommand(doctorCmd)
	Cmd.AddCommand(reportCmd)
	Cmd.AddCommand(pushCmd)
	Cmd.AddCommand(rolloutCmd)
	Cmd.AddCommand(cleanCmd)
	Cmd.AddCommand(recoverRaftCmd)
	Cmd.AddCommand(enrollCmd)
	Cmd.AddCommand(unlockCmd)
	Cmd.AddCommand(migrateConfCmd)
	Cmd.AddCommand(setupCmd)
}
