package shared

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/DeBrosOfficial/network/cmd/orama/internal/clierr"
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
		bearer, err := auth.BearerFromEnv(gatewayURL, token)
		if err != nil {
			return "", envTokenError(err)
		}
		return bearer, nil
	}

	store, err := auth.LoadEnhancedCredentials()
	if err != nil {
		return "", fmt.Errorf("failed to load credentials: %w", err)
	}

	creds := store.GetDefaultCredential(gatewayURL)
	if creds == nil {
		return "", clierr.Auth("no credentials found for %s. Run 'orama auth login' to authenticate", gatewayURL)
	}
	return auth.Bearer(gatewayURL, store, creds)
}

// envTokenError is the exit code for an ORAMA_TOKEN that could not become a
// bearer: the gateway refusing it is an auth failure, like a missing login.
// Anything else (no route to the gateway, a token that cannot be sent) keeps
// the code it already had.
func envTokenError(err error) error {
	var refusal *auth.GatewayError
	if errors.As(err, &refusal) && (refusal.Status == http.StatusUnauthorized || refusal.Status == http.StatusForbidden) {
		return clierr.Wrap(clierr.CodeAuth, err)
	}
	return err
}

// GetAuthToken returns the credential for commands that have no --gateway flag.
func GetAuthToken() (string, error) {
	return AuthToken("")
}

// BearerNamespace asks the gateway which namespace a bearer belongs to. The
// stored session cannot answer for a credential that came from ORAMA_TOKEN,
// and is the wrong answer when the two differ.
func BearerNamespace(gatewayURL, token string) (string, error) {
	raw, _, err := RequestWith(httpClient, gatewayURL, token, http.MethodGet, "/v1/auth/whoami", nil)
	if err != nil {
		switch StatusOf(err) {
		case http.StatusUnauthorized, http.StatusForbidden:
			return "", clierr.Auth("the gateway does not accept this credential: %w", err)
		case 0:
			return "", clierr.Unavailable("find the namespace of this credential: %w", err)
		}
		return "", clierr.Failure("find the namespace of this credential: %w", err)
	}
	var out struct {
		Authenticated bool   `json:"authenticated"`
		Namespace     string `json:"namespace"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return "", clierr.Failure("could not read the gateway's answer: %w", err)
	}
	if !out.Authenticated {
		return "", clierr.Auth("the gateway does not recognise this credential: run 'orama auth login'")
	}
	return out.Namespace, nil
}
