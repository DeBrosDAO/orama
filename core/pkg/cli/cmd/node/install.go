package node

import (
	"github.com/DeBrosOfficial/network/pkg/cli/production/install"
	"github.com/spf13/cobra"
)

var installCmd = &cobra.Command{
	Use:   "install",
	Short: "Install production node (requires sudo)",
	Long: `Install and configure an Orama production node on this machine.
For the first node, this creates a new cluster. For subsequent nodes,
use --join and --token to join an existing cluster.`,
	Run: func(cmd *cobra.Command, args []string) {
		install.Handle(args)
	},
	DisableFlagParsing: true, // Pass flags through to existing handler
}
