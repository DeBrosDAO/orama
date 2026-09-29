//go:build e2e_fleet

package gatewaymiddlewarechaos

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/features/internal/edge"
	"github.com/DeBrosOfficial/network/e2e/harness"
	"github.com/DeBrosOfficial/network/e2e/harness/fleet"
	"github.com/DeBrosOfficial/network/e2e/harness/gw"
	"github.com/DeBrosOfficial/network/e2e/harness/wallet"
)

// The credential bucket: 30 a minute per address, burst 10, per gateway
// (core/pkg/gateway/gateway.go configureRateLimiters); challenges also 10 a
// minute, burst 5, per wallet (handlers/auth/wallet_rate_limit.go).
const (
	credBurst      = 10
	challengeBurst = 5
	// floodCeiling bounds how many requests a flood sends looking for its
	// first 429: the burst, plus what refills while it runs.
	floodCeiling = credBurst + 5
	tokenPath    = "/v1/auth/token"
	challenge    = "/v1/auth/challenge"
	// retryAfterCred is the credential bucket's Retry-After, in seconds.
	retryAfterCred = "60"
)

// rpcError is the 429 envelope (core/pkg/httputil/rpc_error.go).
type rpcError struct {
	OK    bool `json:"ok"`
	Error struct {
		Code       string  `json:"code"`
		Message    string  `json:"message"`
		Retryable  bool    `json:"retryable"`
		RetryAfter float64 `json:"retry_after"`
	} `json:"error"`
}

// quiet takes the run's credential burst before a flood and again after it,
// so the flood meets a full product bucket and leaves one behind for the
// run's paced clients (edge.Quiesce).
func quiet(t *testing.T) {
	t.Helper()
	host := gatewayHost(t)
	if err := edge.Quiesce(t.Context(), host); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := fleet.CleanupContext(t)
		defer cancel()
		if err := edge.Quiesce(ctx, host); err != nil {
			t.Errorf("cleanup: %v", err)
		}
	})
}

func gatewayHost(t *testing.T) string {
	t.Helper()
	u, err := url.Parse(harness.Fleet(t).State.GatewayURL)
	if err != nil {
		t.Fatal(err)
	}
	return u.Hostname()
}

// flood sends req unpaced to c until the first 429 and returns it, with how
// many were admitted before it.
func flood(t *testing.T, c *gw.Client, req gw.Req) (*gw.Response, int) {
	t.Helper()
	for i := range floodCeiling {
		resp := c.MustSend(t, req)
		if resp.Status == http.StatusTooManyRequests {
			return resp, i
		}
	}
	t.Fatalf("%s: %d requests from one address, no 429 (the credential bucket bursts to %d)", req.Path, floodCeiling, credBurst)
	return nil, 0
}

// TestRateLimit_credentialBucketPerAddress: from one address the credential
// routes admit the burst and then answer 429 with Retry-After: 60, the coded
// retryable envelope and the security headers (docs/ARCHITECTURE.md
// "Middleware Stack": 30 a minute per address bursting to 10).
func TestRateLimit_credentialBucketPerAddress(t *testing.T) {
	quiet(t)
	f := harness.Fleet(t)
	c := harness.GW(t).Unpaced().PinTo(f.State.Nodes[0].PublicIP)
	resp, admitted := flood(t, c, gw.Req{Method: http.MethodPost, Path: tokenPath, Header: http.Header{"Content-Type": {"application/json"}}, Body: []byte(`{}`)})
	if admitted < credBurst-1 {
		t.Errorf("429 after only %d requests, want the burst of %d admitted", admitted, credBurst)
	}
	requireCredential429(t, resp)
	if other := harness.GW(t).Unpaced().PinTo(f.State.Nodes[1].PublicIP).MustSend(t, gw.Req{Method: http.MethodPost, Path: tokenPath, Body: []byte(`{}`)}); other.Status == http.StatusTooManyRequests {
		t.Errorf("%s's bucket is empty although only %s was flooded: the limiter is per gateway", f.State.Nodes[1].Name, f.State.Nodes[0].Name)
	}
	if h := c.MustSend(t, gw.Req{Path: "/health"}); h.Status != http.StatusOK {
		t.Errorf("/health from the limited address: %d, want 200 (the general bucket is 10,000 a minute)", h.Status)
	}
}

func requireCredential429(t *testing.T, resp *gw.Response) {
	t.Helper()
	var body rpcError
	if err := json.Unmarshal(resp.Body, &body); err != nil || body.OK || body.Error.Code != "RATE_LIMITED" || !body.Error.Retryable || body.Error.RetryAfter != 60 {
		t.Errorf("429 body %s (%v), want {ok:false, error:{code:RATE_LIMITED, retryable:true, retry_after:60}}", resp.Body, err)
	}
	if got := resp.Header.Get("Retry-After"); got != retryAfterCred {
		t.Errorf("Retry-After %q, want %s", got, retryAfterCred)
	}
	for _, h := range []string{"X-Content-Type-Options", "X-Frame-Options", "Strict-Transport-Security"} {
		if resp.Header.Get(h) == "" {
			t.Errorf("the 429 lacks %s", h)
		}
	}
}

// TestRateLimit_spoofedForwardedForDoesNotMoveTheBucket: a client that
// writes its own X-Forwarded-For is still charged to its address — Caddy
// appends the real peer, and only the last entry is read (docs/SECURITY.md
// "Rate limiting").
func TestRateLimit_spoofedForwardedForDoesNotMoveTheBucket(t *testing.T) {
	quiet(t)
	f := harness.Fleet(t)
	c := harness.GW(t).Unpaced().PinTo(f.State.Nodes[1].PublicIP)
	for i := range floodCeiling {
		resp := c.MustSend(t, gw.Req{Method: http.MethodPost, Path: tokenPath, Body: []byte(`{}`),
			Header: http.Header{"X-Forwarded-For": {fmt.Sprintf("203.0.113.%d, 198.51.100.%d", i+1, i+1)}, "X-Real-Ip": {"192.0.2.1"}}})
		if resp.Status == http.StatusTooManyRequests {
			requireCredential429(t, resp)
			return
		}
	}
	t.Fatalf("%d requests each claiming another X-Forwarded-For were never limited: the header chose the bucket", floodCeiling)
}

// TestRateLimit_challengePerWallet: challenges for one wallet stop at the
// wallet's burst of 5 even while the address still has credential budget,
// with the per-wallet refusal (docs/SECURITY.md "Rate limiting": a
// challenge writes a row for a wallet the caller need not own).
func TestRateLimit_challengePerWallet(t *testing.T) {
	quiet(t)
	f := harness.Fleet(t)
	w, err := wallet.NewEVM()
	if err != nil {
		t.Fatal(err)
	}
	c := harness.GW(t).Unpaced().PinTo(f.State.Nodes[2].PublicIP)
	body, _ := json.Marshal(map[string]string{"wallet": w.Address()})
	req := gw.Req{Method: http.MethodPost, Path: challenge, Header: http.Header{"Content-Type": {"application/json"}}, Body: body}
	for i := range challengeBurst {
		if resp := c.MustSend(t, req); resp.Status != http.StatusOK {
			t.Fatalf("challenge %d of the wallet's burst: %d %.200s", i+1, resp.Status, resp.Body)
		}
	}
	resp := c.MustSend(t, req)
	var out struct {
		Error string `json:"error"`
	}
	if resp.Status != http.StatusTooManyRequests || json.Unmarshal(resp.Body, &out) != nil || out.Error == "" || resp.Header.Get("Retry-After") != retryAfterCred {
		t.Fatalf("challenge %d for one wallet: %d %s Retry-After %q, want the per-wallet 429", challengeBurst+1, resp.Status, resp.Body, resp.Header.Get("Retry-After"))
	}
	other, _ := json.Marshal(map[string]string{"wallet": mustWallet(t)})
	if r := c.MustSend(t, gw.Req{Method: http.MethodPost, Path: challenge, Header: req.Header, Body: other}); r.Status != http.StatusOK {
		t.Errorf("another wallet from the same address: %d, want 200 (the address still has budget)", r.Status)
	}
}

func mustWallet(t *testing.T) string {
	t.Helper()
	w, err := wallet.NewEVM()
	if err != nil {
		t.Fatal(err)
	}
	return w.Address()
}

// nodeFlood sends n POSTs of {} to url from node in one shell loop (so the
// bucket does not refill between SSH round trips) and returns the statuses.
func nodeFlood(t *testing.T, f *fleet.Fleet, node fleet.Node, url string, header string, n int) []int {
	t.Helper()
	h := ""
	if header != "" {
		h = " -H " + fleet.ShellQuote(header)
	}
	cmd := fmt.Sprintf("for i in $(seq %d); do curl -s -o /dev/null -w '%%{http_code}\\n' --max-time 10 -X POST%s --data '{}' %s; done",
		n, h, fleet.ShellQuote(url))
	out := f.MustExec(t, node, cmd)
	var codes []int
	for _, line := range strings.Fields(out.Stdout) {
		c, err := strconv.Atoi(line)
		if err != nil {
			t.Fatalf("%s: unreadable status %q", node.Name, line)
		}
		codes = append(codes, c)
	}
	if len(codes) != n {
		t.Fatalf("%s: %d statuses for %d requests: %s", node.Name, len(codes), n, out.Stdout)
	}
	return codes
}

// localFlood is enough requests to empty the credential bucket twice over.
const localFlood = 2 * floodCeiling

// TestRateLimit_loopbackWithForwardedForIsNotExempt: every public request
// reaches the gateway from 127.0.0.1 through Caddy, so loopback with a
// forwarding header is charged to the forwarded address and limited; plain
// loopback (a process on the node) and another node over the overlay are
// exempt (docs/ARCHITECTURE.md "Middleware Stack"; docs/SECURITY.md "Rate
// limiting").
func TestRateLimit_loopbackWithForwardedForIsNotExempt(t *testing.T) {
	quiet(t)
	f := harness.Fleet(t)
	a, b := f.State.Nodes[0], f.State.Nodes[1]
	forwarded := nodeFlood(t, f, a, edge.LocalGateway(tokenPath), "X-Forwarded-For: 203.0.113.77", localFlood)
	if !slices.Contains(forwarded, http.StatusTooManyRequests) {
		t.Errorf("%d loopback requests forwarded for 203.0.113.77 were never limited: %v", localFlood, forwarded)
	}
	if plain := nodeFlood(t, f, a, edge.LocalGateway(tokenPath), "", localFlood); slices.Contains(plain, http.StatusTooManyRequests) {
		t.Errorf("plain loopback was limited: %v", plain)
	}
	if overlay := nodeFlood(t, f, a, edge.OverlayGateway(b, tokenPath), "", localFlood); slices.Contains(overlay, http.StatusTooManyRequests) {
		t.Errorf("%s -> %s over the overlay was limited: %v", a.Name, b.Name, overlay)
	}
}
