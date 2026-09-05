// Package invitecmd provides `orama invite`, which mints an invite from the
// operator's own machine.
//
// Minting one used to mean SSHing to an existing node and running
// `sudo orama node invite` there. The gateway has had POST /v1/operator/invite
// all along; only `orama node setup` used it, and never showed the operator
// the token it got.
package invitecmd

import (
	"encoding/json"
	"time"

	"github.com/DeBrosOfficial/network/pkg/cli"
	"github.com/DeBrosOfficial/network/pkg/cli/clierr"
	"github.com/DeBrosOfficial/network/pkg/cli/printer"
	"github.com/DeBrosOfficial/network/pkg/cli/shared"
	"github.com/DeBrosOfficial/network/pkg/invite"
	"github.com/spf13/cobra"
)

var flags struct {
	expiry time.Duration
	env    string
}

// defaultExpiry is how long an invite lives when the operator does not say.
// Long enough to provision a VPS and run the install, short enough that a token
// left in scrollback is not a standing key to the cluster.
const defaultExpiry = time.Hour

// Cmd is the top-level "invite" command.
var Cmd = &cobra.Command{
	Use:   "invite",
	Short: "Mint an invite for a new node",
	Long: `Create a single-use invite that lets a new node join the cluster.

The invite carries the gateway to join and the fingerprint of its TLS
certificate, so the joining node pins the cluster it was actually invited to
rather than trusting whatever certificate it is first shown. There is nothing
else to copy across.

This is the same token as 'orama node invite', which does the same thing from
an existing node instead of from here.`,
	Example: `  orama invite
  orama invite --expiry 24h
  orama invite --env testnet`,
	Args: cobra.NoArgs,
	RunE: run,
}

func init() {
	Cmd.Flags().DurationVar(&flags.expiry, "expiry", defaultExpiry, "How long the invite stays usable")
	Cmd.Flags().StringVar(&flags.env, "env", "", "Environment to invite into (default: active)")
}

func run(cmd *cobra.Command, args []string) error {
	if flags.expiry <= 0 {
		return clierr.Usage("--expiry must be positive")
	}

	gatewayURL, err := gatewayFor(flags.env)
	if err != nil {
		return err
	}

	raw, err := shared.Request("POST", "/v1/operator/invite", map[string]int{
		"expiry_minutes": int(flags.expiry.Minutes()),
	})
	if err != nil {
		return err
	}

	var resp struct {
		Token     string `json:"token"`
		ExpiresAt string `json:"expires_at"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return clierr.Failure("could not parse the gateway's reply: %w", err)
	}
	if resp.Token == "" {
		return clierr.Failure("the gateway returned no token")
	}

	// Read the certificate the joining node will be shown, so it can pin it.
	// A failure here is reported rather than producing an invite with no
	// pinning: an invite that silently drops to trust-on-first-use is worse
	// than one the operator has to retry.
	fingerprint, err := invite.Fingerprint(gatewayURL)
	if err != nil {
		return clierr.Unavailable("could not read the gateway's TLS certificate: %w\n"+
			"  The joining node needs its fingerprint to verify the cluster", err)
	}

	encoded, err := invite.Encode(invite.Invite{
		JoinURL:       gatewayURL,
		Token:         resp.Token,
		CAFingerprint: fingerprint,
	})
	if err != nil {
		return clierr.Failure("could not encode the invite: %w", err)
	}

	out := printer.For(cmd)
	if out.JSONMode() {
		return out.JSON(map[string]string{
			"invite":         encoded,
			"join_url":       gatewayURL,
			"expires_at":     resp.ExpiresAt,
			"ca_fingerprint": fingerprint,
		})
	}

	out.Printf("Invite created, usable until %s\n\n", resp.ExpiresAt)
	out.Printf("Run this on the new node:\n\n")
	out.Printf("  sudo orama node install --token %s --vps-ip <NEW_NODE_IP>\n\n", encoded)
	out.Printf("Or from here:\n\n")
	out.Printf("  orama node install --remote --token %s --vps-ip <NEW_NODE_IP>\n", encoded)
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
