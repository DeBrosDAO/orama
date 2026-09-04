package node

import (
	"github.com/spf13/cobra"
)

// Cmd is the root command for node operator commands (was "prod").
var Cmd = &cobra.Command{
	Use:   "node",
	Short: "Node operator commands",
	Long: `Operate Orama nodes, both the one on this machine and the fleet you own.

Local, run on the node itself and needing root (sudo):
  install, uninstall, upgrade, start, stop, restart, status, logs, doctor,
  report, invite, unlock, schema, migrate, migrate-raft-id, migrate-conf

Remote, run from your machine and reaching nodes over SSH:
  list, setup, enroll, push, rollout, clean, decommission, wipe, recover-raft

The remote commands are the same implementations as the top-level 'orama push',
'orama rollout' and 'orama nodes'.`,
}

func init() {
	Cmd.AddCommand(listCmd)
	Cmd.AddCommand(installCmd)
	Cmd.AddCommand(uninstallCmd)
	Cmd.AddCommand(upgradeCmd)
	Cmd.AddCommand(startCmd)
	Cmd.AddCommand(stopCmd)
	Cmd.AddCommand(restartCmd)
	Cmd.AddCommand(statusCmd)
	Cmd.AddCommand(logsCmd)
	Cmd.AddCommand(inviteCmd)
	Cmd.AddCommand(migrateRaftIDCmd)
	Cmd.AddCommand(migrateCmd)
	Cmd.AddCommand(doctorCmd)
	Cmd.AddCommand(reportCmd)
	Cmd.AddCommand(pushCmd)
	Cmd.AddCommand(rolloutCmd)
	Cmd.AddCommand(cleanCmd)
	Cmd.AddCommand(decommissionCmd)
	Cmd.AddCommand(wipeCmd)
	Cmd.AddCommand(recoverRaftCmd)
	Cmd.AddCommand(enrollCmd)
	Cmd.AddCommand(unlockCmd)
	Cmd.AddCommand(migrateConfCmd)
	Cmd.AddCommand(setupCmd)
	Cmd.AddCommand(schemaCmd)
}
