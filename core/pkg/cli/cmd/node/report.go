package node

import (
	"github.com/DeBrosOfficial/network/pkg/cli/production/report"
	"github.com/spf13/cobra"
)

var reportCmd = &cobra.Command{
	Use:   "report",
	Short: "Output comprehensive node health data as JSON",
	Long: `Collect all system and service data from this node and output
as a single JSON blob. Designed to be called by 'orama monitor' over SSH.
Requires root privileges for full data collection.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		jsonFlag, _ := cmd.Flags().GetBool("json")
		return report.Handle(jsonFlag, "")
	},
}

func init() {
	reportCmd.Flags().Bool("json", true, "Output as JSON (default)")
}
