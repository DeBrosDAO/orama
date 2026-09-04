package node

import (
	"github.com/DeBrosOfficial/network/pkg/cli/production/logs"
	"github.com/spf13/cobra"
)

var logsFollow bool

var logsCmd = &cobra.Command{
	Use:   "logs <service>",
	Short: "View production service logs",
	Long: `Stream logs for a specific Orama production service.
Service aliases: node, ipfs, cluster, gateway, olric`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return logs.Run(args[0], logsFollow)
	},
}

func init() {
	logsCmd.Flags().BoolVarP(&logsFollow, "follow", "f", false, "Stream new log lines as they arrive")
}
