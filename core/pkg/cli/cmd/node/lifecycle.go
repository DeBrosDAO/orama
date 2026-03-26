package node

import (
	"github.com/DeBrosOfficial/network/pkg/cli/production/lifecycle"
	"github.com/spf13/cobra"
)

var forceFlag bool

var startCmd = &cobra.Command{
	Use:   "start",
	Short: "Start all production services (requires sudo)",
	Run: func(cmd *cobra.Command, args []string) {
		lifecycle.HandleStart()
	},
}

var stopCmd = &cobra.Command{
	Use:   "stop",
	Short: "Stop all production services (requires sudo)",
	Long: `Stop all Orama services in dependency order and disable auto-start.
Includes namespace services, global services, and supporting services.
Use --force to bypass quorum safety check.`,
	Run: func(cmd *cobra.Command, args []string) {
		force, _ := cmd.Flags().GetBool("force")
		lifecycle.HandleStopWithFlags(force)
	},
}

var restartCmd = &cobra.Command{
	Use:   "restart",
	Short: "Restart all production services (requires sudo)",
	Long: `Restart all Orama services. Stops in dependency order then restarts.
Includes explicit namespace service restart.
Use --force to bypass quorum safety check.`,
	Run: func(cmd *cobra.Command, args []string) {
		force, _ := cmd.Flags().GetBool("force")
		lifecycle.HandleRestartWithFlags(force)
	},
}

func init() {
	stopCmd.Flags().Bool("force", false, "Bypass quorum safety check")
	restartCmd.Flags().Bool("force", false, "Bypass quorum safety check")
}
