package node

import (
	"github.com/DeBrosOfficial/network/pkg/cli/production/status"
	"github.com/spf13/cobra"
)

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show production service status",
	Run: func(cmd *cobra.Command, args []string) {
		status.Handle()
	},
}
