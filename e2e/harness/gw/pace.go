package gw

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/DeBrosOfficial/network/e2e/harness/pace"
)

// Device-code and device-approval routes, rate-limited with the credential
// routes (core/pkg/gateway/rate_limit_key.go isAuthRateLimitPath).
const (
	PathDevice         = "/v1/auth/device"
	PathDeviceToken    = "/v1/auth/device/token"
	PathDeviceApprove  = "/v1/auth/device/approve"
	PathDevicesApprove = "/v1/auth/devices/approve"
)

// credentialPaths are the routes the gateway's per-address credential
// limiter counts.
var credentialPaths = map[string]bool{
	PathChallenge: true, PathVerify: true, PathAPIKey: true, PathToken: true, PathRefresh: true,
	PathDevice: true, PathDeviceToken: true, PathDeviceApprove: true, PathDevicesApprove: true,
}

// IsCredentialPath reports whether the gateway's per-address credential
// limiter counts requests to path. Every request to one is paced (see Unpaced).
func IsCredentialPath(path string) bool { return credentialPaths[path] }

// PacingError is a 429 on a paced credential request. The harness keeps the
// whole run under the product's limits, so it is never retried: it means the
// limiter regressed or something unpaced spent the same address's budget.
type PacingError struct {
	Host   string
	Status *StatusError
}

func (e *PacingError) Error() string {
	return fmt.Sprintf("pacing exceeded: %s answered 429 to a paced credential request although the harness pacer "+
		"(e2e/harness/pace) keeps the run under the product's credential limits; either the gateway's limiter "+
		"regressed or something unpaced (a gw.Unpaced client, a flood test, an orama command the CLI runner does "+
		"not pace) spent this address's budget: %v", e.Host, e.Status)
}

// Unwrap exposes the 429 itself.
func (e *PacingError) Unwrap() error { return e.Status }

// pacingErr is the error of a paced request's response: a *PacingError for a
// 429, nil otherwise.
func pacingErr(req *http.Request, resp *Response) error {
	if resp.Status != http.StatusTooManyRequests {
		return nil
	}
	return &PacingError{Host: req.URL.Hostname(),
		Status: &StatusError{Method: req.Method, Path: req.URL.Path, Status: resp.Status, Body: string(resp.Body)}}
}

// Unpaced returns a copy of c that sends credential requests without waiting
// on the run's pacer, and whose 429s are plain responses. It is ONLY for tests
// that deliberately exercise the gateway's rate limiter (flood tests); every
// other test must stay paced, or it spends budget other packages count on.
func (c *Client) Unpaced() *Client {
	cp := *c
	cp.unpaced = true
	return &cp
}

// WithPacer returns a copy of c paced by p (nil: not paced). Clients built by
// New/NewWithTLS in a fleet run already use the run's pacer.
func (c *Client) WithPacer(p *pace.Pacer) *Client {
	cp := *c
	cp.pacer = p
	return &cp
}

// paceRequest waits for the tokens req spends when it goes to a credential
// route: the per-wallet challenge bucket (for a challenge naming a wallet)
// and the per-address bucket. It reports whether req was paced.
func (c *Client) paceRequest(ctx context.Context, req *http.Request, body []byte) (bool, error) {
	if c.unpaced || c.pacer == nil || !credentialPaths[req.URL.Path] {
		return false, nil
	}
	host := req.URL.Hostname()
	if req.URL.Path == PathChallenge {
		if w := challengeWallet(body); w != "" {
			if err := c.pacer.Wait(ctx, host, pace.ChallengeBucket(w)); err != nil {
				return true, fmt.Errorf("%s %s: %w", req.Method, req.URL.Path, err)
			}
		}
	}
	if err := c.pacer.Wait(ctx, host, pace.BucketCred); err != nil {
		return true, fmt.Errorf("%s %s: %w", req.Method, req.URL.Path, err)
	}
	return true, nil
}

// challengeWallet is the wallet a challenge body names, or "" when the body
// is not a JSON object with one (a negative test's garbage).
func challengeWallet(body []byte) string {
	var b struct {
		Wallet string `json:"wallet"`
	}
	if json.Unmarshal(body, &b) != nil {
		return ""
	}
	return strings.TrimSpace(b.Wallet)
}

// rawRequestPath is the path (without query) of a raw HTTP/1.1 request's
// request line, or "" when it has none.
func rawRequestPath(request []byte) string {
	line, _, _ := bytes.Cut(request, []byte("\n"))
	fields := strings.Fields(string(line))
	if len(fields) < 2 {
		return ""
	}
	path, _, _ := strings.Cut(fields[1], "?")
	return path
}

// paceRaw waits for an address token when a raw request goes to a credential
// route (the per-wallet bucket is not parsed out of raw bytes).
func (c *Client) paceRaw(ctx context.Context, host string, request []byte) error {
	if c.unpaced || c.pacer == nil || !credentialPaths[rawRequestPath(request)] {
		return nil
	}
	if err := c.pacer.Wait(ctx, host, pace.BucketCred); err != nil {
		return fmt.Errorf("raw request to %s: %w", host, err)
	}
	return nil
}

// Protect registers secret (a credential the test obtained some other way)
// with the evidence redactor and, through it, the run's token registry, so it
// is masked in this process, in the runner and in the report.
func (c *Client) Protect(secret string) error {
	if strings.TrimSpace(secret) == "" {
		return fmt.Errorf("refusing to protect an empty secret")
	}
	return c.protect(secret)
}
