// Package invitecmd provides `orama invite`, which mints an invite from the
// operator's own machine.
//
// Minting one used to mean SSHing to an existing node and running
// `sudo orama node invite` there. The gateway has had POST /v1/operator/invite
// all along; only `orama node setup` used it, and never showed the operator
// the token it got.
package invitecmd

import (
	"time"

	"github.com/DeBrosOfficial/network/cmd/orama/internal"
	"github.com/DeBrosOfficial/network/cmd/orama/internal/clierr"
	"github.com/DeBrosOfficial/network/cmd/orama/internal/invitemint"
	"github.com/DeBrosOfficial/network/cmd/orama/internal/printer"
	"github.com/DeBrosOfficial/network/cmd/orama/internal/shared"
	"github.com/spf13/cobra"
)

var flags struct {
	expiry time.Duration
	env    string
	node   string
}

// defaultExpiry is how long an invite lives when the operator does not say.
// Long enough to provision a VPS and run the install, short enough that a token
// left in scrollback is not a standing key to the cluster. The gateway caps it
// at an hour.
const defaultExpiry = time.Hour

// Cmd is the top-level "invite" command.
var Cmd = &cobra.Command{
	Use:   "invite",
	Short: "Mint an invite for a new node",
	Long: `Create a single-use invite that lets a new node join the cluster.

The invite names one node of the cluster: its public address, the domain to
present to it, and the fingerprint of the TLS certificate it serves. The
joining node connects to exactly that node and pins exactly that certificate,
rather than resolving the cluster's domain — which reaches any nameserver,
each with a certificate of its own — or trusting whatever certificate it is
first shown. There is nothing else to copy across.

The node is the lowest address the environment's domain resolves to, or the
one named with --node. The token is minted through that node, on the same
connection whose certificate is pinned.

This is the same token as 'orama node invite', which does the same thing from
an existing node instead of from here.`,
	Example: `  orama invite
  orama invite --expiry 30m
  orama invite --env testnet
  orama invite --env testnet --node 203.0.113.7`,
	Args: cobra.NoArgs,
	RunE: run,
}

func init() {
	Cmd.Flags().DurationVar(&flags.expiry, "expiry", defaultExpiry, "How long the invite stays usable (the gateway caps it at 1h)")
	Cmd.Flags().StringVar(&flags.env, "env", "", "Environment to invite into (default: active)")
	Cmd.Flags().StringVar(&flags.node, "node", "", "Public IP of the node the invite names (default: the lowest address the environment's domain resolves to)")
}

func run(cmd *cobra.Command, args []string) error {
	if flags.expiry <= 0 {
		return clierr.Usage("--expiry must be positive")
	}

	gatewayURL, err := gatewayFor(flags.env)
	if err != nil {
		return err
	}
	host, err := invitemint.GatewayHost(gatewayURL)
	if err != nil {
		return clierr.Usage("%v", err)
	}
	nodeIP, err := invitemint.ChooseNode(cmd.Context(), flags.node, host)
	if err != nil {
		return clierr.Unavailable("%w (name the node with --node <public IP>)", err)
	}
	// The credential belongs to the gateway of the environment being invited
	// into, not to the active one.
	bearer, err := shared.AuthToken(gatewayURL)
	if err != nil {
		return err
	}
	m, err := invitemint.MintThrough(gatewayURL, host, nodeIP, bearer, flags.expiry)
	if err != nil {
		return clierr.Unavailable("%w\n  Name another node of the cluster with --node <public IP>", err)
	}

	out := printer.For(cmd)
	if out.JSONMode() {
		return out.JSON(map[string]string{
			"invite":         m.Invite,
			"join_url":       m.JoinURL,
			"sni":            m.SNI,
			"expires_at":     m.ExpiresAt,
			"ca_fingerprint": m.Fingerprint,
		})
	}

	out.Printf("Invite created through %s (%s), usable until %s\n\n", m.NodeIP, m.SNI, m.ExpiresAt)
	out.Printf("Run this on the new node:\n\n")
	out.Printf("  sudo orama node install --token %s --vps-ip <NEW_NODE_IP>\n\n", m.Invite)
	out.Printf("Or from here:\n\n")
	out.Printf("  orama node install --remote --token %s --vps-ip <NEW_NODE_IP>\n", m.Invite)
	return nil
}

// gatewayFor resolves the gateway an invite is minted against.
func gatewayFor(env string) (string, error) {
	if env == "" {
		return shared.GetAPIURL()
	}
	e, err := cli.GetEnvironmentByName(env)
	if err != nil {
		return "", clierr.NotFound("environment %q not found: %w", env, err)
	}
	return e.GatewayURL, nil
}
