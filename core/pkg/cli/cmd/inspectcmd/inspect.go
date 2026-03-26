package inspectcmd

import (
	"github.com/DeBrosOfficial/network/pkg/cli"
	"github.com/spf13/cobra"
)

// Cmd is the inspect command for SSH-based cluster inspection.
var Cmd = &cobra.Command{
	Use:   "inspect",
	Short: "Inspect cluster health via SSH",
	Long: `SSH into cluster nodes and run health checks.
Supports AI-powered failure analysis and result export.`,
	Run: func(cmd *cobra.Command, args []string) {
		cli.HandleInspectCommand(args)
	},
	DisableFlagParsing: true, // Pass all flags through to existing handler
}
