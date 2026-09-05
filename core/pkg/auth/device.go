package auth

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/DeBrosOfficial/network/pkg/tlsutil"
)

// Signing in from a machine with no wallet on it.
//
// The client half of the device authorization grant. The waiting machine asks
// for a code and polls; a human approves that code from somewhere their wallet
// already is. See docs/AUTH.md for the exchange, and the gateway's
// pkg/gateway/auth/device.go for what it is protecting against.

// DeviceLogin is a pending login this machine is waiting on.
type DeviceLogin struct {
	// DeviceCode is this machine's own credential for collecting the session.
	// It is never printed: what the human reads is the user code.
	DeviceCode string
	// UserCode is what to type on the machine that has the wallet.
	UserCode string
	// ExpiresAt is when waiting stops being worth it.
	ExpiresAt time.Time
	// Interval is the shortest gap between polls.
	Interval time.Duration
}

// deviceOutcome is the vocabulary RFC 8628 §3.5 gives the polling client.
const (
	outcomePending  = "authorization_pending"
	outcomeSlowDown = "slow_down"
	outcomeExpired  = "expired_token"
	outcomeDenied   = "access_denied"
)

// StartDeviceLogin asks the gateway for a pending login.
func StartDeviceLogin(gatewayURL, namespace string) (*DeviceLogin, error) {
	payload, err := json.Marshal(map[string]string{"namespace": strings.TrimSpace(namespace)})
	if err != nil {
		return nil, fmt.Errorf("encode the request: %w", err)
	}

	body, status, err := deviceRequest(gatewayURL, "/v1/auth/device", payload)
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, GatewayErrorFrom(status, body)
	}

	var out struct {
		DeviceCode string `json:"device_code"`
		UserCode   string `json:"user_code"`
		ExpiresIn  int    `json:"expires_in"`
		Interval   int    `json:"interval"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("read the response: %w", err)
	}
	if out.DeviceCode == "" || out.UserCode == "" {
		return nil, fmt.Errorf("this gateway answered without a device code; it is older than this CLI and " +
			"does not support signing in without a wallet on the machine")
	}

	login := &DeviceLogin{
		DeviceCode: out.DeviceCode,
		UserCode:   out.UserCode,
		ExpiresAt:  time.Now().Add(time.Duration(out.ExpiresIn) * time.Second),
		Interval:   time.Duration(out.Interval) * time.Second,
	}
	if login.Interval <= 0 {
		login.Interval = 5 * time.Second
	}
	return login, nil
}

// PollDeviceLogin waits for somebody to approve, and returns the session.
//
// progress is called once per poll so a caller can show that something is still
// happening; it is not the place decisions are made.
func PollDeviceLogin(gatewayURL string, login *DeviceLogin, progress func()) (*Credentials, error) {
	payload, err := json.Marshal(map[string]string{"device_code": login.DeviceCode})
	if err != nil {
		return nil, fmt.Errorf("encode the request: %w", err)
	}

	interval := login.Interval
	for {
		if time.Now().After(login.ExpiresAt) {
			return nil, fmt.Errorf("nobody approved this login within %s; run 'orama auth login' again for a new code",
				DeviceLoginWindow)
		}
		time.Sleep(interval)
		if progress != nil {
			progress()
		}

		body, status, err := deviceRequest(gatewayURL, "/v1/auth/device/token", payload)
		if err != nil {
			return nil, err
		}
		if status == http.StatusOK {
			return credentialsFromDeviceToken(gatewayURL, body)
		}

		switch deviceOutcomeOf(body) {
		case outcomePending:
			continue
		case outcomeSlowDown:
			// The RFC's own remedy. Backing off by the interval is what stops
			// a client that is already too fast from staying too fast.
			interval += login.Interval
		case outcomeExpired:
			return nil, fmt.Errorf("this login expired before anybody approved it; run 'orama auth login' again")
		case outcomeDenied:
			return nil, fmt.Errorf("the approver refused this login")
		default:
			return nil, GatewayErrorFrom(status, body)
		}
	}
}

// DeviceLoginWindow is how long a pending login stands, mirrored from the
// gateway so the message about running out of time can say how long it had.
const DeviceLoginWindow = 10 * time.Minute

// ApproveDeviceLogin approves — or refuses — a pending login, from a machine
// that does have a wallet.
//
// It signs the gateway's own challenge, which is the same proof signing in
// takes. Refusing costs the same signature as approving: refusing is how you
// would stop somebody else's login, and it should be no cheaper.
func ApproveDeviceLogin(gatewayURL, userCode, namespace string, deny bool) (wallet string, err error) {
	if !IsRootWalletInstalled() {
		return "", fmt.Errorf("approving a login needs a wallet on this machine, and the RootWallet agent " +
			"is not reachable here; approve from a machine where it is")
	}

	wallet, err = getRootWalletAddress()
	if err != nil {
		return "", fmt.Errorf("failed to get wallet address: %w", err)
	}

	client := tlsutil.NewHTTPClientForDomain(sessionHTTPTimeout, extractDomainFromURL(gatewayURL))
	message, err := requestChallenge(client, gatewayURL, wallet, namespace)
	if err != nil {
		return "", fmt.Errorf("failed to get challenge: %w", err)
	}

	signature, err := signWithRootWallet(message)
	if err != nil {
		return "", fmt.Errorf("failed to sign challenge: %w", err)
	}

	payload, err := json.Marshal(map[string]any{
		"user_code": userCode,
		"message":   message,
		"signature": signature,
		"deny":      deny,
	})
	if err != nil {
		return "", fmt.Errorf("encode the request: %w", err)
	}

	body, status, err := deviceRequest(gatewayURL, "/v1/auth/device/approve", payload)
	if err != nil {
		return "", err
	}
	if status != http.StatusOK {
		return "", GatewayErrorFrom(status, body)
	}
	return wallet, nil
}

// credentialsFromDeviceToken turns a collected session into a stored one.
//
// There is no API key in it, and that is the point: what a login hands back is
// a session that expires, not a credential to keep.
func credentialsFromDeviceToken(gatewayURL string, body []byte) (*Credentials, error) {
	var out sessionResponse
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("read the response: %w", err)
	}
	if out.AccessToken == "" {
		return nil, fmt.Errorf("the gateway approved this login and answered without a token")
	}

	creds := &Credentials{
		Namespace:    out.Namespace,
		UserID:       out.Subject,
		Wallet:       out.Subject,
		IssuedAt:     time.Now(),
		NamespaceURL: namespaceGatewayURL(gatewayURL, out.Namespace),
	}
	creds.SetSession(out.AccessToken, out.RefreshToken, out.ExpiresIn)
	return creds, nil
}

// namespaceGatewayURL is where a namespace's own gateway lives.
func namespaceGatewayURL(gatewayURL, namespace string) string {
	domain := extractDomainFromURL(gatewayURL)
	if domain == "" || namespace == "" {
		return ""
	}
	if namespace == "default" {
		return "https://" + domain
	}
	return fmt.Sprintf("https://ns-%s.%s", namespace, domain)
}

// deviceOutcomeOf reads the `error` member the polling client switches on.
func deviceOutcomeOf(body []byte) string {
	var out struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return ""
	}
	return out.Error
}

// deviceRequest posts to one of the device endpoints and returns the body and
// status, because every one of them says something in a non-200.
func deviceRequest(gatewayURL, path string, payload []byte) ([]byte, int, error) {
	client := tlsutil.NewHTTPClientForDomain(sessionHTTPTimeout, extractDomainFromURL(gatewayURL))

	resp, err := client.Post(gatewayURL+path, "application/json", bytes.NewReader(payload))
	if err != nil {
		return nil, 0, fmt.Errorf("reach %s: %w", gatewayURL+path, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if err != nil {
		return nil, resp.StatusCode, fmt.Errorf("read the response from %s: %w", path, err)
	}
	return body, resp.StatusCode, nil
}
