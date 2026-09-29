//go:build e2e_fleet

package authsignin

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/e2e/harness/eventually"
	"github.com/DeBrosOfficial/network/e2e/harness/fleet"
	"github.com/DeBrosOfficial/network/e2e/harness/gw"
	"github.com/DeBrosOfficial/network/e2e/harness/wallet"
)

// Documented lifetimes and intervals this package asserts on.
const (
	// accessTokenLifetime: "The access token lasts 15 minutes" (docs/AUTH.md#signing-in).
	accessTokenLifetime = 15 * time.Minute
	// refreshTokenLifetime: "The refresh token lasts 30 days" (docs/AUTH.md#signing-in).
	refreshTokenLifetime = 30 * 24 * time.Hour
	// challengeTTL is the nonce's life (core/pkg/gateway/auth/challenge.go ChallengeTTL).
	challengeTTL = 5 * time.Minute
	// revocationStaleness: "reloaded every 10 seconds" (docs/AUTH.md#revoking).
	revocationStaleness = 10 * time.Second
	// stalenessSlack covers the request round trip on top of the staleness.
	stalenessSlack = 5 * time.Second
	// refreshReuseGrace is how long a just-rotated refresh token is accepted
	// once more (core/pkg/gateway/auth/service.go refreshReuseGrace).
	refreshReuseGrace = 60 * time.Second
	// lifetimeTolerance absorbs clock differences when comparing lifetimes.
	lifetimeTolerance = 90 * time.Second
	// pollEvery is how often revocation and clock waits are re-checked.
	pollEvery = time.Second
	// lobby is the namespace a challenge without one signs in to.
	lobby = gw.LobbyNamespace
)

// expectRefusal fails unless resp has status and code. Every 401 and 403
// must carry a non-empty error, code and hint (docs/AUTH.md#when-a-request-is-refused).
func expectRefusal(t testing.TB, resp *gw.Response, status int, code string) map[string]any {
	t.Helper()
	var body map[string]any
	if err := json.Unmarshal(resp.Body, &body); err != nil {
		t.Fatalf("want HTTP %d %s with a JSON body, got %d: %q", status, code, resp.Status, resp.Body)
	}
	if resp.Status != status || body["code"] != code {
		t.Fatalf("want HTTP %d code %s, got %d: %s", status, code, resp.Status, resp.Body)
	}
	for _, k := range []string{"error", "code", "hint"} {
		if s, _ := body[k].(string); strings.TrimSpace(s) == "" {
			t.Errorf("HTTP %d %s lacks a non-empty %q: %s", status, code, k, resp.Body)
		}
	}
	if status == http.StatusUnauthorized && resp.Header.Get("WWW-Authenticate") == "" {
		t.Errorf("401 %s carries no WWW-Authenticate header", code)
	}
	return body
}

// postJSON sends v as a JSON body.
func postJSON(t testing.TB, c *gw.Client, path, bearer string, v any) *gw.Response {
	t.Helper()
	body, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("failed to encode %s body: %v", path, err)
	}
	return c.MustSend(t, gw.Req{Method: http.MethodPost, Path: path, Bearer: bearer,
		Header: http.Header{"Content-Type": {"application/json"}}, Body: body})
}

// newWallet is a fresh EVM wallet.
func newWallet(t testing.TB) *wallet.EVM {
	t.Helper()
	w, err := wallet.NewEVM()
	if err != nil {
		t.Fatal(err)
	}
	return w
}

// challengeFor asks c for a challenge and fails the test when none is issued.
func challengeFor(t testing.TB, c *gw.Client, req gw.ChallengeRequest) *gw.ChallengeResponse {
	t.Helper()
	ch, _, err := c.For(t).Challenge(t.Context(), req)
	if err != nil {
		t.Fatalf("challenge for %s in %q: %v", req.Wallet, req.Namespace, err)
	}
	return ch
}

// signed is a message and a wallet's EIP-191 signature over it.
func signed(t testing.TB, w *wallet.EVM, message string) gw.VerifyRequest {
	t.Helper()
	sig, err := w.Sign(message)
	if err != nil {
		t.Fatal(err)
	}
	return gw.VerifyRequest{Message: message, Signature: sig}
}

// signIn is a lobby (namespace "") or namespace sign-in that must succeed.
func signIn(t testing.TB, c *gw.Client, w *wallet.EVM, namespace string) *gw.Session {
	t.Helper()
	s, err := c.For(t).SignIn(t.Context(), w, namespace, nil)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// mutated signs a changed copy of an issued challenge message.
func mutated(t testing.TB, w *wallet.EVM, message string, change func(*wallet.SIWEMessage)) gw.VerifyRequest {
	t.Helper()
	text, err := wallet.Mutate(message, change)
	if err != nil {
		t.Fatal(err)
	}
	return signed(t, w, text)
}

// nodeClient is a gateway client whose connections all go to one node, for
// cross-gateway assertions: the public name resolves to every node.
type nodeClient struct {
	Node   fleet.Node
	Client *gw.Client
}

// perNode returns c pinned to each core node in turn, same URL, same trust.
func perNode(t testing.TB, f *fleet.Fleet, c *gw.Client) []nodeClient {
	t.Helper()
	out := make([]nodeClient, 0, len(f.State.Nodes))
	for _, n := range f.State.Nodes {
		out = append(out, nodeClient{Node: n, Client: c.PinTo(n.PublicIP)})
	}
	return out
}

// waitUntil is a time-expiry wait: it blocks until the wall clock passes at,
// for a server-side lifetime (a nonce, a reuse grace) that only elapsed time
// ends and no endpoint reports. It polls the clock itself because the lint
// bans sleeps and the clock is the only readiness signal there is.
func waitUntil(t testing.TB, at time.Time, what string) {
	t.Helper()
	budget := time.Until(at) + 2*pollEvery
	if budget <= 0 {
		return
	}
	eventually.Require(t, pollEvery, budget, what, func() (bool, error) {
		return time.Now().After(at), nil
	})
}

// jwtClaims decodes a JWT's header and payload without verifying it: the
// test reads what the gateway put there, the gateway is what verifies.
func jwtClaims(t testing.TB, token string) (header, payload map[string]any) {
	t.Helper()
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		t.Fatalf("access token is not a three-part JWT (%d parts)", len(parts))
	}
	header, payload = map[string]any{}, map[string]any{}
	for i, dst := range []map[string]any{header, payload} {
		raw, err := base64.RawURLEncoding.DecodeString(parts[i])
		if err != nil {
			t.Fatalf("JWT part %d is not base64url: %v", i, err)
		}
		if err := json.Unmarshal(raw, &dst); err != nil {
			t.Fatalf("JWT part %d is not JSON: %v", i, err)
		}
	}
	return header, payload
}

// claimTime reads a numeric JWT claim as a time.
func claimTime(t testing.TB, payload map[string]any, name string) time.Time {
	t.Helper()
	v, ok := payload[name].(float64)
	if !ok {
		t.Fatalf("JWT has no numeric %q claim: %v", name, payload)
	}
	return time.Unix(int64(v), 0)
}

// whoami is GET /v1/auth/whoami with bearer.
func whoami(t testing.TB, c *gw.Client, bearer string) *gw.Response {
	t.Helper()
	return c.MustSend(t, gw.Req{Path: gw.PathWhoami, Bearer: bearer})
}

// refusedEverywhere waits until every node refuses bearer with AUTH_REVOKED,
// and fails when that takes longer than the revocation list's staleness.
func refusedEverywhere(t testing.TB, nodes []nodeClient, bearer string) {
	t.Helper()
	start := time.Now()
	for _, nc := range nodes {
		eventually.Require(t, pollEvery, revocationStaleness+stalenessSlack, nc.Node.Name+" to refuse the revoked token", func() (bool, error) {
			resp := whoami(t, nc.Client, bearer)
			if resp.Status == http.StatusUnauthorized && resp.ErrorCode() == "AUTH_REVOKED" {
				return true, nil
			}
			return false, fmt.Errorf("HTTP %d %s", resp.Status, resp.ErrorCode())
		})
	}
	if took := time.Since(start); took > revocationStaleness+stalenessSlack {
		t.Errorf("revocation took %s to reach every gateway, the promise is %s", took, revocationStaleness)
	}
}

// containsFold reports whether s contains sub, ignoring case.
func containsFold(s, sub string) bool {
	return strings.Contains(strings.ToLower(s), strings.ToLower(sub))
}
