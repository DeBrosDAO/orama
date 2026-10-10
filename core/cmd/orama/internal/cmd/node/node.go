package node

import (
	"github.com/DeBrosOfficial/network/cmd/orama/internal/cmdmeta"
	"github.com/DeBrosOfficial/network/cmd/orama/internal/production/push"
	"github.com/spf13/cobra"
)

// Cmd is the root command for node operator commands (was "prod").
var Cmd = &cobra.Command{
	Use:   "node",
	Short: "Node operator commands",
	Long: `Operate Orama nodes, both the one on this machine and the fleet you own.

Local, run on the node itself and needing root (sudo):
  uninstall, upgrade, start, stop, restart, status, logs, doctor, report, invite,
  trust

Remote, run from your machine and reaching nodes over SSH:
  list, setup, remove, wipe, dns delegation

Installing a node's software, staging an archive, auto-update, recovery and
migration are maintainer commands: see 'orama maint node'.`,
}

// MaintCmd is `orama maint node`: the node commands a maintainer, an installer
// or a unit runs, which an operator does not type.
var MaintCmd = &cobra.Command{
	Use:   "node",
	Short: "Install, stage, recover and migrate nodes",
	Long: `Node commands for maintainers and for the installer.

Install and stage a release on this machine, simulate the auto-update decision,
recover a cluster that lost its raft quorum, migrate older state, apply gateway
schema migrations, and enroll or unlock an OramaOS node.`,
}

func init() {
	Cmd.AddCommand(listCmd)
	Cmd.AddCommand(uninstallCmd)
	Cmd.AddCommand(upgradeCmd)
	Cmd.AddCommand(startCmd)
	Cmd.AddCommand(stopCmd)
	Cmd.AddCommand(restartCmd)
	Cmd.AddCommand(statusCmd)
	Cmd.AddCommand(logsCmd)
	Cmd.AddCommand(inviteCmd)
	Cmd.AddCommand(doctorCmd)
	Cmd.AddCommand(reportCmd)
	Cmd.AddCommand(decommissionCmd)
	Cmd.AddCommand(wipeCmd)
	Cmd.AddCommand(dnsCmd)
	Cmd.AddCommand(setupCmd)

	MaintCmd.AddCommand(installCmd)
	MaintCmd.AddCommand(migrateRaftIDCmd)
	MaintCmd.AddCommand(recoverRaftCmd)
	MaintCmd.AddCommand(enrollCmd)
	MaintCmd.AddCommand(unlockCmd)
	MaintCmd.AddCommand(migrateConfCmd)
	MaintCmd.AddCommand(schemaCmd)
	MaintCmd.AddCommand(push.NewStageArchiveCmd())

	// One release's CLI runs these two over SSH on nodes whose installed orama
	// is another release's: `orama maint push` and `orama node setup` run
	// `orama node stage-archive`, and a remote install runs `orama node
	// install`, each on the machine's own binary, which a rollback or a first
	// upgrade makes older than the CLI that asks. The commands moved to
	// `orama maint node`; the path the remote callers use stays, hidden.
	Cmd.AddCommand(cmdmeta.HiddenAlias(installCmd))
	stageArchive := push.NewStageArchiveCmd()
	stageArchive.Hidden = true
	Cmd.AddCommand(stageArchive)
}
