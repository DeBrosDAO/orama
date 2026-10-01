package shared

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

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
//
// The one exception is NamespaceGatewayURL, for the routes only a namespace's
// own gateway serves. The credential is still the one AuthToken derives from
// the gateway GatewayURL resolves: a short-lived session of the signed-in
// namespace, which the namespace's gateway verifies against the same cluster
// key, never the API key that is stored for the cluster gateway. Pointing the
// CLI at another gateway with ORAMA_API_URL still names both the URL and the
// credential.

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

// NamespaceGatewayURL returns the namespace gateway the session is signed in
// to, for the routes only a namespace's own gateway serves: its raw database
// (/v1/rqlite/export and /import) and backup and restore. The gateway that
// fronts the cluster answers those 503 or 403, because its database is the
// registry and not the tenant's.
//
// The URL comes from the default credential, which login stored with the
// namespace's host. With ORAMA_TOKEN, or a gateway with no stored namespace
// host, it is the gateway GatewayURL resolves, so pointing ORAMA_API_URL at
// the namespace's host still works.
func NamespaceGatewayURL() (string, error) {
	gatewayURL, err := GetAPIURL()
	if err != nil {
		return "", err
	}
	if envToken() != "" {
		return gatewayURL, nil
	}
	store, err := auth.LoadEnhancedCredentials()
	if err != nil {
		return "", fmt.Errorf("failed to load credentials: %w", err)
	}
	if creds := store.GetDefaultCredential(gatewayURL); creds != nil && creds.NamespaceURL != "" {
		return creds.NamespaceURL, nil
	}
	return gatewayURL, nil
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
	return renewedBearer(gatewayURL, store, creds)
}

// AuthTokenFor is AuthToken for a named namespace: the credential a command
// given --namespace acts with. An empty namespace is AuthToken's own answer.
//
// Namespace-scoped routes act on the credential's namespace, so the flag has
// to choose the credential. It used to be read and ignored, and a command aimed
// at one namespace acted on whichever one the current session was in. An
// ORAMA_TOKEN belongs to one namespace; asked for another, it is refused
// rather than used on the wrong one.
func AuthTokenFor(override, namespace string) (string, error) {
	namespace = strings.TrimSpace(namespace)
	if namespace == "" {
		return AuthToken(override)
	}
	gatewayURL, err := GatewayURL(override)
	if err != nil {
		return "", err
	}
	if envToken() != "" {
		bearer, err := AuthToken(override)
		if err != nil {
			return "", err
		}
		have, err := BearerNamespace(gatewayURL, bearer)
		if err != nil {
			return "", err
		}
		if have != namespace {
			return "", clierr.Usage("ORAMA_TOKEN belongs to namespace %q, not %q: use a token of %q or drop --namespace", have, namespace, namespace)
		}
		return bearer, nil
	}
	store, err := auth.LoadEnhancedCredentials()
	if err != nil {
		return "", fmt.Errorf("failed to load credentials: %w", err)
	}
	creds := store.CredentialForNamespace(gatewayURL, namespace)
	if creds == nil {
		return "", clierr.Auth("not signed in to namespace %q at %s: run 'orama auth login --namespace %s'", namespace, gatewayURL, namespace)
	}
	return renewedBearer(gatewayURL, store, creds)
}

// renewedBearer is auth.Bearer with the failure classified for the exit code:
// a session the gateway ended, or a key it refused, is an authentication
// failure like a missing login. A renewal that failed without the gateway
// judging the session (unreachable, 5xx, 429) keeps the code it had, because
// the session is intact and the command may be retried.
func renewedBearer(gatewayURL string, store *auth.EnhancedCredentialStore, creds *auth.Credentials) (string, error) {
	bearer, err := auth.Bearer(gatewayURL, store, creds)
	if err == nil {
		return bearer, nil
	}
	var refusal *auth.GatewayError
	if errors.Is(err, auth.ErrSessionEnded) ||
		(errors.As(err, &refusal) && (refusal.Status == http.StatusUnauthorized || refusal.Status == http.StatusForbidden)) {
		return "", clierr.Wrap(clierr.CodeAuth, err)
	}
	return "", err
}

// envTokenError is the exit code for an ORAMA_TOKEN that could not become a
// bearer: the gateway refusing it is an auth failure, like a missing login.
// Anything else (no route to the gateway, a token that cannot be sent) keeps
// the code it already had.
func envTokenError(err error) error {
	var refusal *auth.GatewayError
	switch {
	case errors.As(err, &refusal) && (refusal.Status == http.StatusUnauthorized || refusal.Status == http.StatusForbidden):
		return clierr.Wrap(clierr.CodeAuth, err)
	case errors.As(err, &refusal) && refusal.IsRetryable():
		// A rate limit or a gateway without a leader: the key may be fine,
		// and the same command may work in a moment.
		return clierr.Wrap(clierr.CodeUnavailable, err)
	case errors.Is(err, auth.ErrGatewayUnreachable):
		return clierr.Wrap(clierr.CodeUnavailable, err)
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
