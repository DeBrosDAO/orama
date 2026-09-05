package shared

import (
	"fmt"

	"github.com/DeBrosOfficial/network/pkg/auth"
)

// Gateway resolution for every command that calls a gateway.
//
// The URL a request goes to and the credential attached to it must come from
// the same decision. They used to come from two: the URL honoured
// ORAMA_API_URL while the credential lookup did not, so pointing the CLI at
// one gateway sent it the API key stored for another — a request to a tenant
// gateway carrying the operator's key for the default one. Four copies of that
// pair existed, so fixing it in one place fixed nothing.
//
// Resolution order is environment variable, then the active environment in
// ~/.orama/environments.json, then an error. See auth.ResolveGatewayURL.

// GatewayURL returns the gateway this command talks to. override, when
// non-empty, comes from an explicit --gateway flag and wins over everything.
func GatewayURL(override string) (string, error) {
	if override != "" {
		return override, nil
	}
	return auth.ResolveGatewayURL()
}

// GetAPIURL returns the gateway URL for commands that have no --gateway flag.
func GetAPIURL() (string, error) {
	return GatewayURL("")
}

// AuthToken returns the credential to send to the gateway that resolves from
// the same inputs, so it always belongs to the gateway being called.
//
// It is a short-lived token, not the API key. The key used to be the bearer
// credential of every request the CLI made — a ninety-day credential in front
// of every gateway, in every access log along the way — while the session the
// login had already been handed was thrown away. The key is now presented
// once, to exchange it, and only when there is no session to renew.
func AuthToken(override string) (string, error) {
	gatewayURL, err := GatewayURL(override)
	if err != nil {
		return "", err
	}

	if token := envToken(); token != "" {
		return auth.BearerFromEnv(gatewayURL, token)
	}

	store, err := auth.LoadEnhancedCredentials()
	if err != nil {
		return "", fmt.Errorf("failed to load credentials: %w", err)
	}

	creds := store.GetDefaultCredential(gatewayURL)
	if creds == nil {
		return "", fmt.Errorf("no credentials found for %s. Run 'orama auth login' to authenticate", gatewayURL)
	}
	return auth.Bearer(gatewayURL, store, creds)
}

// GetAuthToken returns the credential for commands that have no --gateway flag.
func GetAuthToken() (string, error) {
	return AuthToken("")
}
