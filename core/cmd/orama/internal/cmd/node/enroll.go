package node

import (
	"github.com/DeBrosOfficial/network/cmd/orama/internal/production/enroll"
	"github.com/spf13/cobra"
)

var enrollFlags enroll.Flags

var enrollCmd = &cobra.Command{
	Use:   "enroll",
	Short: "Enroll an OramaOS node into the cluster",
	Long: `Enroll a freshly booted OramaOS node into the cluster.

The OramaOS node prints a registration code on its console. Provide that code
along with an invite token. The Gateway pushes cluster configuration
(WireGuard, secrets, peer list) to the node, sealed under the code.

The code is not served over the network. A GET on port 9999 used to return it.

Usage:
  orama maint node enroll --node-ip <ip> --code <code> --token <invite-token> --gateway <url>

--gateway must be an https:// URL: the invite token is a credential and is never
sent in the clear.

The node must be reachable over the public internet on port 9999 (enrollment only).
After enrollment, port 9999 is permanently closed and all communication goes over WireGuard.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		return enroll.Run(&enrollFlags)
	},
}

func init() {
	f := enrollCmd.Flags()
	f.StringVar(&enrollFlags.NodeIP, "node-ip", "", "Public IP of the OramaOS node (required)")
	f.StringVar(&enrollFlags.Code, "code", "", "Registration code from the node's console (required)")
	f.StringVar(&enrollFlags.Token, "token", "", "Invite token for cluster joining (required)")
	f.StringVar(&enrollFlags.GatewayURL, "gateway", "", "Gateway URL (required, e.g. https://gateway.example.com)")
	f.StringVar(&enrollFlags.Env, "env", "production", "Environment name")
	_ = enrollCmd.MarkFlagRequired("code")
	_ = enrollCmd.MarkFlagRequired("node-ip")
	_ = enrollCmd.MarkFlagRequired("token")
	_ = enrollCmd.MarkFlagRequired("gateway")
}
