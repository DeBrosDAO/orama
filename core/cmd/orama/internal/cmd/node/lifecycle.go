package node

import (
	"github.com/DeBrosOfficial/network/cmd/orama/internal/production/lifecycle"
	"github.com/spf13/cobra"
)

var startCmd = &cobra.Command{
	Use:   "start",
	Short: "Start all production services (requires sudo)",
	RunE: func(cmd *cobra.Command, args []string) error {
		return lifecycle.HandleStart()
	},
}

var stopCmd = &cobra.Command{
	Use:   "stop",
	Short: "Stop all production services (requires sudo)",
	Long: `Stop all Orama services in dependency order and disable auto-start.
Includes namespace services, global services, and supporting services.
Use --force to bypass quorum safety check.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		force, _ := cmd.Flags().GetBool("force")
		return lifecycle.HandleStopWithFlags(force)
	},
}

var restartCmd = &cobra.Command{
	Use:   "restart",
	Short: "Restart all production services (requires sudo)",
	Long: `Restart all Orama services. Stops in dependency order then restarts.
Includes explicit namespace service restart.
The auto-update timer, and a run of the update agent already in progress, are
stopped for the restart, and the timer is started again once the node is serving,
so the update agent never runs against a node that is down. When the restart
fails or the node does not come to serve, the timer stays stopped (starting it
would fire the agent at once against a node that is down) and the output names
the command that starts it. An interrupt, a terminate or a hangup finishes the
restart, then starts the timer if the node serves; a timer that was stopped stays
stopped.
Use --force to bypass quorum safety check.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		force, _ := cmd.Flags().GetBool("force")
		return lifecycle.HandleRestartWithFlags(force)
	},
}

func init() {
	stopCmd.Flags().Bool("force", false, "Bypass quorum safety check")
	restartCmd.Flags().Bool("force", false, "Bypass quorum safety check")
}
