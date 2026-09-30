package auth

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/DeBrosOfficial/network/pkg/tlsutil"
)

// ErrSessionEnded is what a renewal returns when the gateway refused the
// refresh token and there is no key to fall back to: the stored session is
// gone and only a new sign-in brings it back. Callers that classify a failure
// (the CLI's exit code) match it with errors.Is rather than reading the text.
var ErrSessionEnded = errors.New("this session has ended")

// A session, rather than a key on every request.
//
// The CLI stored an API key at login and sent it as the bearer credential of
// every request afterwards. That put a ninety-day credential in front of every
// gateway the CLI was ever pointed at, in every access log along the way, and
// left the JWT and refresh token the login had *already* returned on the floor:
// `verifySignature` read them out of the response and threw them away.
//
// It holds the session now. An access token lasts fifteen minutes and is
// refreshed transparently; the API key is exchanged for one when that is all
// there is — a `ORAMA_TOKEN` in CI, or a credential file written by an older
// CLI.

const (
	// accessTokenRenewalMargin is how long before expiry the token is
	// replaced. A request that starts inside the margin would otherwise arrive
	// after the token it carries has expired.
	accessTokenRenewalMargin = 60 * time.Second

	// sessionHTTPTimeout bounds a refresh or an exchange. It is short because
	// it happens in front of somebody else's request.
	sessionHTTPTimeout = 30 * time.Second
)

// sessionMu serialises the refresh so two commands in one process do not both
// rotate the refresh token — one of them would lose the race and be told it is
// replaying a stolen token. Across processes the credential file's lock does
// the same; see renewStoredSession.
var sessionMu sync.Mutex

// HasLiveAccessToken reports whether the stored access token is still worth
// sending.
func (creds *Credentials) HasLiveAccessToken() bool {
	if creds == nil || strings.TrimSpace(creds.AccessToken) == "" {
		return false
	}
	if creds.AccessTokenExpiresAt.IsZero() {
		return false
	}
	return time.Now().Add(accessTokenRenewalMargin).Before(creds.AccessTokenExpiresAt)
}

// SetSession records what a login, a refresh or an exchange returned.
func (creds *Credentials) SetSession(accessToken, refreshToken string, expiresIn int) {
	creds.AccessToken = accessToken
	if refreshToken != "" {
		creds.RefreshToken = refreshToken
	}
	if expiresIn > 0 {
		creds.AccessTokenExpiresAt = time.Now().Add(time.Duration(expiresIn) * time.Second)
	}
}

// Bearer returns the credential to put in an Authorization header, renewing the
// session when it has to and writing the result back to disk.
//
// Order matters and is not a fallback chain: each step is what to do when the
// step before it has nothing to offer. A live access token is used; a refresh
// token buys a new one; an API key is exchanged for one. Only the last of those
// sends a long-lived credential anywhere, and only when the session is all
// there is.
//
// With a store, the renewal is single-flight across goroutines and processes:
// see renewStoredSession. Without one (a credential held only in memory) it is
// single-flight within this process only.
func Bearer(gatewayURL string, store *EnhancedCredentialStore, creds *Credentials) (string, error) {
	if creds == nil {
		return "", fmt.Errorf("no credential for %s: run 'orama auth login'", gatewayURL)
	}

	sessionMu.Lock()
	defer sessionMu.Unlock()

	if creds.HasLiveAccessToken() {
		return creds.AccessToken, nil
	}
	if store == nil {
		return renewSession(gatewayURL, creds)
	}
	return renewStoredSession(gatewayURL, creds)
}

// renewSession buys creds a new access token, recording what the gateway
// answered in creds. The caller holds sessionMu and persists creds.
//
// A refresh that fails for any reason other than the gateway refusing the
// refresh token itself — it could not be reached, it failed (5xx), it was
// busy (429) — leaves the session exactly as it was and returns the error, so
// the next attempt presents the same refresh token. Only a refusal ends the
// session: see refreshTokenRejected.
func renewSession(gatewayURL string, creds *Credentials) (string, error) {
	client := sessionClient(gatewayURL)

	if strings.TrimSpace(creds.RefreshToken) != "" {
		session, err := refreshSession(client, gatewayURL, creds.RefreshToken, creds.Namespace)
		if err == nil {
			creds.SetSession(session.AccessToken, session.RefreshToken, session.ExpiresIn)
			return creds.AccessToken, nil
		}
		if !refreshTokenRejected(err) {
			return "", fmt.Errorf("could not renew the session with %s, which is kept for the next attempt: %w",
				gatewayURL, err)
		}
		// A refresh token that the gateway refuses is spent, replayed or
		// revoked, and keeping it means every command from here on pays for
		// one more failed round trip before falling through to the key.
		creds.RefreshToken = ""
		if strings.TrimSpace(creds.APIKey) == "" {
			return "", fmt.Errorf("%w (%w); run 'orama auth login'", ErrSessionEnded, err)
		}
	}

	if strings.TrimSpace(creds.APIKey) == "" {
		return "", fmt.Errorf("no usable credential for %s: run 'orama auth login'", gatewayURL)
	}

	session, err := exchangeKey(client, gatewayURL, creds.APIKey)
	if err != nil {
		return "", err
	}
	creds.SetSession(session.AccessToken, session.RefreshToken, session.ExpiresIn)
	return creds.AccessToken, nil
}

// refreshTokenRejected reports whether a failed refresh means the refresh
// token is dead, as opposed to the attempt having failed.
//
// /v1/auth/refresh answers 401 for a token that is unknown, expired, revoked
// or replayed (a rotated token presented again), and 401 or 403 when a
// device-bound session is refused for its device's sake (no proof, revoked,
// pending, or a namespace policy that now requires a device) — none of which
// the CLI can cure by asking again. It answers 503 for a transient failure
// (the rqlite leader moving during a rolling restart), 400 only for a request
// body it could not read (the token was never judged), and something in front
// of it can answer 429 or a proxy's 5xx. Those, and a gateway that could not be
// reached at all, say nothing about the token.
func refreshTokenRejected(err error) bool {
	var gwErr *GatewayError
	if !errors.As(err, &gwErr) {
		return false
	}
	return gwErr.Status == http.StatusUnauthorized || gwErr.Status == http.StatusForbidden
}

// sessionClient is the client for a refresh or a key exchange. It follows no
// redirect: each request carries a refresh token or an API key, and Go
// forwards the Authorization header on a redirect to a subdomain (a tenant's
// app) or to plain http. A 3xx is returned as the unexpected answer it is.
func sessionClient(gatewayURL string) *http.Client {
	client := tlsutil.NewHTTPClientForDomain(sessionHTTPTimeout, extractDomainFromURL(gatewayURL))
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return client
}

// sessionResponse is what /v1/auth/refresh and /v1/auth/token both answer with.
type sessionResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int    `json:"expires_in"`
	Subject      string `json:"subject"`
	Namespace    string `json:"namespace"`
}

func refreshSession(client *http.Client, gatewayURL, refreshToken, namespace string) (*sessionResponse, error) {
	payload, err := json.Marshal(map[string]string{
		"refresh_token": refreshToken,
		"namespace":     namespace,
	})
	if err != nil {
		return nil, fmt.Errorf("encode the refresh request: %w", err)
	}
	return postForSession(client, gatewayURL+"/v1/auth/refresh", payload, "")
}

// exchangeKey trades an API key for a token carrying that key's own grants.
//
// It is the only request in the CLI that sends the key itself, which is the
// point: everything after it carries a credential that expires.
func exchangeKey(client *http.Client, gatewayURL, apiKey string) (*sessionResponse, error) {
	return postForSession(client, gatewayURL+"/v1/auth/token", []byte(`{}`), apiKey)
}

func postForSession(client *http.Client, url string, payload []byte, bearer string) (*sessionResponse, error) {
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("build the request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("reach %s: %w", url, err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if resp.StatusCode != http.StatusOK {
		return nil, GatewayErrorFrom(resp.StatusCode, body)
	}

	var out sessionResponse
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("read the response from %s: %w", url, err)
	}
	if strings.TrimSpace(out.AccessToken) == "" {
		return nil, fmt.Errorf("%s answered without a token", url)
	}
	return &out, nil
}

// LooksLikeJWT reports whether a credential is already a token.
//
// It is a shape test, not a validation: what it decides is whether the CLI
// should exchange the value it was given or send it. A key sent where a token
// was expected is refused by the gateway with a clear code, and a token sent to
// the exchange endpoint is too — but only after a round trip, and only in a
// message about the wrong thing.
func LooksLikeJWT(credential string) bool {
	credential = strings.TrimSpace(credential)
	return strings.Count(credential, ".") == 2 && strings.HasPrefix(credential, "ey")
}

// BearerFromEnv turns the credential named by ORAMA_TOKEN into one to send.
//
// It takes either. A token is sent as it is. A key is exchanged for one, so a
// CI job's key is presented once per run rather than on every request it makes
// — the same trade the stored session makes, minus the file to keep it in.
func BearerFromEnv(gatewayURL, credential string) (string, error) {
	credential = strings.TrimSpace(credential)
	if credential == "" {
		return "", fmt.Errorf("%s is set to an empty value", TokenEnvVar)
	}
	if LooksLikeJWT(credential) {
		return credential, nil
	}

	client := sessionClient(gatewayURL)
	session, err := exchangeKey(client, gatewayURL, credential)
	if err != nil {
		return "", fmt.Errorf("%s could not be exchanged for a session: %w", TokenEnvVar, err)
	}
	return session.AccessToken, nil
}

// TokenEnvVar names a pre-issued credential, so a CI job can run without a
// wallet session. It holds either an API key or a token; see BearerFromEnv.
const TokenEnvVar = "ORAMA_TOKEN"
