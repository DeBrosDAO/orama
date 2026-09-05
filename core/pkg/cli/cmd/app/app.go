package app

import (
	"github.com/DeBrosOfficial/network/pkg/cli/deployments"
	"github.com/spf13/cobra"
)

// Cmd is the root command for managing deployed applications (was "deployments").
var Cmd = &cobra.Command{
	Use:     "app",
	Aliases: []string{"apps"},
	Short:   "Manage deployed applications",
	Long:    `List, get, delete, rollback, and view logs/stats for your deployed applications.`,
}

func init() {
	Cmd.AddCommand(deployments.ListCmd)
	Cmd.AddCommand(deployments.GetCmd)
	Cmd.AddCommand(deployments.DeleteCmd)
	Cmd.AddCommand(deployments.RollbackCmd)
	Cmd.AddCommand(deployments.LogsCmd)
	Cmd.AddCommand(deployments.StatsCmd)
	Cmd.AddCommand(deployments.EnvCmd)
	Cmd.AddCommand(deployments.GrantsCmd)
}
