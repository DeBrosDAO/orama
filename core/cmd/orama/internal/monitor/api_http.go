package monitor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/DeBrosOfficial/network/cmd/orama/internal/clierr"
	"github.com/DeBrosOfficial/network/cmd/orama/internal/noderesolver"
	"github.com/DeBrosOfficial/network/pkg/tlsutil"
)

// Operator telemetry endpoints on the cluster gateway.
const (
	telemetryPath       = "/v1/operator/telemetry"
	telemetryStreamPath = "/v1/operator/telemetry/stream"
)

// Response size bounds.
const (
	// maxErrorBodyBytes is how much of a failed response is read for its
	// message.
	maxErrorBodyBytes = 4 << 10
	// maxSnapshotBytes bounds one snapshot, whether it arrives as a one-shot
	// body or as one event of the stream, so a snapshot the one-shot view can
	// read never makes the live view reconnect forever.
	maxSnapshotBytes = 64 << 20
)

// Transport timeouts. The client has no overall timeout, since a stream lives
// as long as its context; these bound the parts that must not hang instead.
const (
	// dialTimeout bounds opening the TCP connection.
	dialTimeout = 10 * time.Second
	// tlsHandshakeTimeout bounds the TLS handshake.
	tlsHandshakeTimeout = 10 * time.Second
	// responseHeaderTimeout bounds the wait for the answer's headers. The
	// stream sends them with its first snapshot, which a gateway with a cold
	// cache takes up to 20s to assemble.
	responseHeaderTimeout = 45 * time.Second
)

// apiClient makes authenticated calls to one environment's gateway.
type apiClient struct {
	env        string
	gatewayURL string
	// token hands out the bearer. It is asked on every request, so a
	// long-lived stream reconnects with a renewed session rather than an
	// expired one.
	token *tokenCache
	http  *http.Client
}

// newAPIClientForEnv resolves the environment's gateway and credentials the
// same way node resolution does.
func newAPIClientForEnv(env string) (*apiClient, error) {
	gw, err := noderesolver.GatewayURLForEnv(env)
	if err != nil {
		return nil, clierr.Usage("cannot find the gateway for environment %q: %v (see `orama env list`; or read the nodes directly with --ssh)", env, err)
	}
	return &apiClient{
		env:        env,
		gatewayURL: strings.TrimRight(gw, "/"),
		token:      newTokenCache(func() (string, error) { return noderesolver.LoadBearer(gw) }),
		http:       newTelemetryHTTPClient(dialTimeout, responseHeaderTimeout),
	}, nil
}

// newTelemetryHTTPClient trusts the environment's CA (ORAMA_CA_CERT_PATH, or
// the environment's ca_file installed at startup), bounds the connect and the
// wait for headers, and follows no redirect: the bearer is an operator
// credential, and Go forwards it on a redirect to a subdomain (a tenant's
// app) or to plain http, so a 3xx is reported as the unexpected answer it is.
func newTelemetryHTTPClient(dial, headers time.Duration) *http.Client {
	return &http.Client{
		Transport: &http.Transport{
			DialContext:           (&net.Dialer{Timeout: dial}).DialContext,
			TLSClientConfig:       tlsutil.GetTLSConfig(),
			TLSHandshakeTimeout:   tlsHandshakeTimeout,
			ResponseHeaderTimeout: headers,
		},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

// get issues an authenticated GET and returns the response when it is 200.
// Any other outcome is an error that says what to do about it.
func (c *apiClient) get(ctx context.Context, pathAndQuery, accept string) (*http.Response, error) {
	token, err := c.token.get()
	if err != nil {
		return nil, tokenError(c.envLabel(), c.gatewayURL, err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.gatewayURL+pathAndQuery, nil)
	if err != nil {
		return nil, fmt.Errorf("build telemetry request for %s: %w", c.gatewayURL, err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", accept)

	resp, err := c.http.Do(req)
	if err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return nil, withSSHHint(clierr.Unavailable("the %s gateway at %s did not answer in time: %v", c.envLabel(), c.gatewayURL, err))
		}
		if ctx.Err() != nil {
			return nil, fmt.Errorf("telemetry request to %s: %w", c.gatewayURL, ctx.Err())
		}
		return nil, withSSHHint(clierr.Unavailable("cannot reach the %s gateway at %s: %v", c.envLabel(), c.gatewayURL, err))
	}
	if resp.StatusCode == http.StatusOK {
		return resp, nil
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized {
		c.token.invalidate()
	}
	body, readErr := io.ReadAll(io.LimitReader(resp.Body, maxErrorBodyBytes))
	if readErr != nil {
		return nil, fmt.Errorf("read HTTP %d response from %s: %w", resp.StatusCode, c.gatewayURL, readErr)
	}
	return nil, statusError(c.envLabel(), c.gatewayURL, resp.StatusCode, body)
}

func (c *apiClient) envLabel() string {
	if c.env == "" {
		return "active"
	}
	return c.env
}

// statusError turns a non-200 telemetry response into an actionable error
// whose exit code says what kind of failure it is.
func statusError(env, gatewayURL string, code int, body []byte) error {
	msg := serverMessage(code, body)
	switch code {
	case http.StatusUnauthorized:
		return clierr.Auth("the %s gateway at %s did not accept the credential (%s); sign in with `orama env use %s` then `orama auth login`",
			env, gatewayURL, msg, env)
	case http.StatusForbidden:
		return clierr.Auth("the %s gateway at %s refused telemetry to this wallet (%s): cluster telemetry is for the cluster's operators",
			env, gatewayURL, msg)
	case http.StatusBadRequest:
		return clierr.Usage("the %s gateway at %s refused the telemetry request (%s)", env, gatewayURL, msg)
	case http.StatusServiceUnavailable:
		return withSSHHint(clierr.Unavailable("the %s gateway at %s is not ready to serve telemetry (%s); try again shortly", env, gatewayURL, msg))
	case http.StatusNotFound:
		return withSSHHint(clierr.NotFound("the %s gateway at %s has no telemetry API (%s): it runs a release older than this CLI", env, gatewayURL, msg))
	default:
		return withSSHHint(clierr.Failure("the %s gateway at %s answered the telemetry request with %s", env, gatewayURL, msg))
	}
}

// serverMessage is "HTTP <code>: <reason>", taking the reason from a JSON
// {"error": "..."} body when there is one and the raw body otherwise.
func serverMessage(code int, body []byte) string {
	var e struct {
		Error string `json:"error"`
	}
	reason := strings.TrimSpace(string(body))
	// Not every failure has a JSON body (a proxy's 502 page, say); one that
	// does not parse is shown as it came.
	if json.Unmarshal(body, &e) == nil && e.Error != "" {
		reason = e.Error
	}
	if reason == "" {
		return fmt.Sprintf("HTTP %d", code)
	}
	return fmt.Sprintf("HTTP %d: %s", code, truncate(CleanText(reason), maxErrorReasonChars))
}

// maxErrorReasonChars bounds a server's error text in a message.
const maxErrorReasonChars = 200
