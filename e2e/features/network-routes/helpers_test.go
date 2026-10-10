//go:build e2e_fleet

package networkroutes

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/features/internal/edge"
	"github.com/DeBrosOfficial/network/e2e/features/internal/infra"
	"github.com/DeBrosOfficial/network/e2e/features/internal/tenancy"
	"github.com/DeBrosOfficial/network/e2e/harness"
	"github.com/DeBrosOfficial/network/e2e/harness/fleet"
	"github.com/DeBrosOfficial/network/e2e/harness/gw"
	"github.com/DeBrosOfficial/network/e2e/harness/ns"
)

// The four network routes (docs/whitepaper/technical-reference/appendices/i-api-surface.md "Network"; handlers in
// core/pkg/gateway/network_handlers.go, policy in route_policy.go).
const (
	pathStatus     = "/v1/network/status"
	pathPeers      = "/v1/network/peers"
	pathConnect    = "/v1/network/connect"
	pathDisconnect = "/v1/network/disconnect"

	// The coordination headers, one per stamp generation. Any of them switches
	// the detail routes to the node-to-node path, which the handler answers 404
	// unless the MAC verifies and the caller is on the overlay
	// (core/pkg/auth HasCoordinationStamp, core/pkg/gateway/network_detail_auth.go).
	// The v3 stamp is the one a node signs once every node is nonced, when the
	// older two are no longer written (the legacy floor); the v1 header is the
	// one a mixed fleet still carries.
	coordinationHeader   = "X-Orama-Coordination-MAC"
	coordinationV3Header = "X-Orama-Coordination-MAC-V3"
	// macHexLen is the hex length of the HMAC-SHA256 the stamp carries.
	macHexLen = 64

	// Scopes a key is minted with (core/pkg/gateway/auth/scopes.go).
	scopeAdmin   = "admin"
	scopeStorage = "storage"
)

// every is all four routes; detail the two reads, mutation the two writes.
var (
	detail   = []string{pathStatus, pathPeers}
	mutation = []string{pathConnect, pathDisconnect}
	every    = append(append([]string{}, detail...), mutation...)
)

// isMutation reports whether path is a POST route.
func isMutation(path string) bool { return path == pathConnect || path == pathDisconnect }

// call sends the route's own method: GET to a detail route, POST with body
// (an empty JSON object when nil: invalid by construction for both writes,
// so no refusal test can move a peer connection) to a mutation route.
func call(t testing.TB, c *gw.Client, path string, who tenancy.Cred, body []byte, extra http.Header) *gw.Response {
	t.Helper()
	req := gw.Req{Method: http.MethodGet, Path: path, Bearer: who.Bearer, APIKey: who.APIKey, Header: http.Header{}}
	if isMutation(path) {
		if body == nil {
			body = []byte(`{}`)
		}
		req.Method, req.Body = http.MethodPost, body
		req.Header.Set("Content-Type", "application/json")
	}
	for k, vs := range extra {
		req.Header[k] = vs
	}
	return c.MustSend(t, req)
}

// expectCode records an error unless resp is status carrying code.
func expectCode(t testing.TB, what string, resp *gw.Response, status int, code string) {
	t.Helper()
	if resp.Status != status || resp.ErrorCode() != code {
		t.Errorf("%s: HTTP %d %q, want %d %s: %.300s", what, resp.Status, resp.ErrorCode(), status, code, resp.Body)
	}
}

// operatorOwner is a fresh namespace whose owner is made an operator of the
// cluster for t (`orama maint operator add`, removed at t's cleanup). Only
// TestNetworkRoutes_asOperator calls it, once, and shares the result with
// its parallel subtests, so the package changes the operator list once: the
// owner's grant holds the operator domain and the wallet is on the operator
// list, the two halves the network routes ask for (route_policy.go "Topology
// mutation and node operation").
func operatorOwner(t *testing.T) *ns.Namespace {
	t.Helper()
	n := tenancy.Namespace(t, harness.Fleet(t), ns.Options{})
	addr := n.Owner.Wallet.Address()
	cli := harness.CLI(t)
	infra.ExpectExit(t, infra.Run(t, cli, "maint", "operator", "add", addr), infra.ExitOK)
	t.Cleanup(func() {
		ctx, cancel := fleet.CleanupContext(t)
		defer cancel()
		if res, err := cli.Run(ctx, "maint", "operator", "remove", addr); err != nil || res.Exit != 0 {
			t.Errorf("cleanup: removing operator %s failed: %v %s", addr, err, res.Stderr)
		}
	})
	return n
}

// peersList is GET /v1/network/peers as who: `/p2p/<id>` followed by that
// peer's addresses, the gateway's own host first (network_handlers.go).
func peersList(t testing.TB, c *gw.Client, who tenancy.Cred) []string {
	t.Helper()
	var body struct {
		Peers []string `json:"peers"`
	}
	if err := call(t, c, pathPeers, who, nil, nil).Expect(t, http.StatusOK).Decode(&body); err != nil {
		t.Fatalf("failed to decode %s: %v", pathPeers, err)
	}
	return body.Peers
}

// remotePeers groups a peers list by peer id, without the gateway's own host.
func remotePeers(list []string) map[string][]string {
	out := map[string][]string{}
	current, seen := "", 0
	for _, entry := range list {
		if id, ok := strings.CutPrefix(entry, "/p2p/"); ok {
			seen++
			current = ""
			if seen > 1 {
				current = id
				out[id] = nil
			}
			continue
		}
		if current != "" {
			out[current] = append(out[current], entry)
		}
	}
	return out
}

// unheldPeer is a well-formed peer id nobody holds, or fails the test.
func unheldPeer(t testing.TB) string {
	t.Helper()
	id, err := edge.UnheldPeerID()
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// jsonBody marshals v or fails the test.
func jsonBody(t testing.TB, v any) []byte {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("failed to encode a request body: %v", err)
	}
	return raw
}
