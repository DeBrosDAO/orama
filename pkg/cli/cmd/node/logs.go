package node

import (
	"github.com/DeBrosOfficial/network/pkg/cli/production/logs"
	"github.com/spf13/cobra"
)

var logsCmd = &cobra.Command{
	Use:   "logs <service>",
	Short: "View production service logs",
	Long: `Stream logs for a specific Orama production service.
Service aliases: node, ipfs, cluster, gateway, olric`,
	Run: func(cmd *cobra.Command, args []string) {
		logs.Handle(args)
	},
	DisableFlagParsing: true,
}
