package node

import (
	"github.com/DeBrosOfficial/network/pkg/cli/production/enroll"
	"github.com/spf13/cobra"
)

var enrollCmd = &cobra.Command{
	Use:   "enroll",
	Short: "Enroll an OramaOS node into the cluster",
	Long: `Enroll a freshly booted OramaOS node into the cluster.

The OramaOS node displays a registration code on port 9999. Provide this code
along with an invite token to complete enrollment. The Gateway pushes cluster
configuration (WireGuard, secrets, peer list) to the node.

Usage:
  orama node enroll --node-ip <ip> --code <code> --token <invite-token> --env <environment>

The node must be reachable over the public internet on port 9999 (enrollment only).
After enrollment, port 9999 is permanently closed and all communication goes over WireGuard.`,
	Run: func(cmd *cobra.Command, args []string) {
		enroll.Handle(args)
	},
	DisableFlagParsing: true,
}
