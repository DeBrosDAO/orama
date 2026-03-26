package cluster

import (
	origCluster "github.com/DeBrosOfficial/network/pkg/cli/cluster"
	"github.com/spf13/cobra"
)

// Cmd is the root command for cluster operations (flattened from cluster rqlite).
var Cmd = &cobra.Command{
	Use:   "cluster",
	Short: "Cluster management and diagnostics",
	Long: `View cluster status, run health checks, manage RQLite Raft state,
and monitor the cluster in real-time.`,
}

var statusSubCmd = &cobra.Command{
	Use:   "status",
	Short: "Show cluster node status (RQLite + Olric)",
	Run: func(cmd *cobra.Command, args []string) {
		origCluster.HandleStatus(args)
	},
}

var healthSubCmd = &cobra.Command{
	Use:   "health",
	Short: "Run cluster health checks",
	Run: func(cmd *cobra.Command, args []string) {
		origCluster.HandleHealth(args)
	},
}

var watchSubCmd = &cobra.Command{
	Use:   "watch",
	Short: "Live cluster status monitor",
	Run: func(cmd *cobra.Command, args []string) {
		origCluster.HandleWatch(args)
	},
	DisableFlagParsing: true,
}

// Flattened rqlite commands (was cluster rqlite <cmd>)
var raftStatusCmd = &cobra.Command{
	Use:   "raft-status",
	Short: "Show detailed Raft state for local node",
	Run: func(cmd *cobra.Command, args []string) {
		origCluster.HandleRQLite([]string{"status"})
	},
}

var votersCmd = &cobra.Command{
	Use:   "voters",
	Short: "Show current voter list",
	Run: func(cmd *cobra.Command, args []string) {
		origCluster.HandleRQLite([]string{"voters"})
	},
}

var backupCmd = &cobra.Command{
	Use:   "backup",
	Short: "Trigger manual RQLite backup",
	Run: func(cmd *cobra.Command, args []string) {
		origCluster.HandleRQLite(append([]string{"backup"}, args...))
	},
	DisableFlagParsing: true,
}

func init() {
	Cmd.AddCommand(statusSubCmd)
	Cmd.AddCommand(healthSubCmd)
	Cmd.AddCommand(watchSubCmd)
	Cmd.AddCommand(raftStatusCmd)
	Cmd.AddCommand(votersCmd)
	Cmd.AddCommand(backupCmd)
}
