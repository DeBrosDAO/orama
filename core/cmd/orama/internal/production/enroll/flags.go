package enroll

import (
	"net/url"

	"github.com/DeBrosOfficial/network/cmd/orama/internal/clierr"
)

// Flags holds the parsed command-line flags for the enroll command.
type Flags struct {
	NodeIP     string // Public IP of the OramaOS node
	Code       string // Registration code from the node's console
	Token      string // Invite token for cluster joining
	GatewayURL string // Gateway HTTPS URL
	Env        string // Environment name (for display only)
}

// validate checks the required flags. Every refusal is a usage error: nothing
// has been sent yet.
func (f *Flags) validate() error {
	if f.NodeIP == "" {
		return clierr.Usage("--node-ip is required")
	}
	if f.Code == "" {
		return clierr.Usage("--code is required: read it from the node's console")
	}
	if f.Token == "" {
		return clierr.Usage("--token is required")
	}
	if f.GatewayURL == "" {
		return clierr.Usage("--gateway is required")
	}
	return validateGatewayURL(f.GatewayURL)
}

// validateGatewayURL refuses a gateway the invite token would travel to in
// the clear. The token is a credential (it admits a node to the cluster), and
// the request carries it in both the body and the Authorization header, so it
// is only ever sent over https.
func validateGatewayURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return clierr.Usage("--gateway %q is not a URL: give the gateway as https://<host>", raw)
	}
	if u.Scheme != "https" {
		return clierr.Usage("--gateway must be https://: the invite token is a credential and is never sent over %s", u.Scheme)
	}
	return nil
}
