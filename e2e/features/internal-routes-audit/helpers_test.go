//go:build e2e_fleet

package internalroutesaudit

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/e2e/features/internal/edge"
	"github.com/DeBrosOfficial/network/e2e/features/internal/infra"
	"github.com/DeBrosOfficial/network/e2e/harness"
	"github.com/DeBrosOfficial/network/e2e/harness/fleet"
)

// Headers the node-to-node routes authenticate with (core/pkg/auth
// coordination.go CoordinationMACHeader, nodeapi.go NodeIDHeader and
// NodeStampHeader; core/pkg/gateway/handlers/wireguard X-Cluster-Secret), and
// the constant the removed coordination header carried (core/pkg/auth
// coordination.go: "X-Orama-Internal-Auth: namespace-coordination").
const (
	headerCoordination  = "X-Orama-Coordination-MAC"
	headerNodeID        = "X-Orama-Node-ID"
	headerNodeStamp     = "X-Orama-Node-Stamp"
	headerClusterSecret = "X-Cluster-Secret"
	headerLegacyAuth    = "X-Orama-Internal-Auth"
	legacyAuthValue     = "namespace-coordination"
	// macHexLen and sigHexLen are an HMAC-SHA256 and an Ed25519 signature in hex.
	macHexLen = 64
	sigHexLen = 128
	// notTheSecret is a cluster secret no cluster has.
	notTheSecret = "e2e-not-the-cluster-secret"
	// oversizeBody is past the node routes' 64 KiB and the WireGuard
	// routes' 1 MiB body bounds (nodeapi maxBodyBytes, wireguard MaxBytesReader).
	oversizeBody = 1<<20 + 64<<10
)

// route is one node-to-node route: the method its handler serves, a body
// and query that are invalid by construction (no secret, no stamp, no
// namespace, a node nobody holds), and what the internet gets.
type route struct {
	path     string
	method   string
	body     string
	query    url.Values
	internet int
	// nodeSelf routes answer 404 to everything from off the host
	// (docs/SECURITY.md "A node recording itself").
	nodeSelf bool
}

// url is the route's path and query.
func (r route) url() string {
	if len(r.query) == 0 {
		return r.path
	}
	return r.path + "?" + r.query.Encode()
}

// routes is every node-to-node route that is not client-reachable, with the
// status the public name owes each: spawn checks the method, then the
// overlay and the MAC (401); the node's own routes refuse off-host callers
// with 404; telemetry is 404 without a verified stamp from the overlay; the
// WireGuard routes refuse a non-overlay source with 403 (handlers in
// core/pkg/gateway).
func routes(t testing.TB) []route {
	t.Helper()
	unheld := unheldPeer(t)
	return []route{
		{path: "/v1/internal/namespace/spawn", method: http.MethodPost, body: `{}`, internet: http.StatusUnauthorized},
		{path: "/v1/internal/node/enrol-key", method: http.MethodPost, body: `{}`, internet: http.StatusNotFound, nodeSelf: true},
		{path: "/v1/internal/node/heartbeat", method: http.MethodPost, body: `{}`, internet: http.StatusNotFound, nodeSelf: true},
		{path: "/v1/internal/node/register", method: http.MethodPost, body: `{}`, internet: http.StatusNotFound, nodeSelf: true},
		{path: "/v1/internal/telemetry", method: http.MethodGet, internet: http.StatusNotFound},
		{path: "/v1/internal/wg/peer", method: http.MethodPost, body: `{"cluster_secret":"` + notTheSecret + `"}`, internet: http.StatusForbidden},
		{path: "/v1/internal/wg/peer/remove", method: http.MethodDelete, query: url.Values{"node_id": {unheld}}, internet: http.StatusForbidden},
		{path: "/v1/internal/wg/peers", method: http.MethodGet, internet: http.StatusForbidden},
	}
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

// forgedStamps are the node-to-node credentials in the right shape, none of
// them made with a key this cluster holds: a coordination MAC, a node stamp
// for a node nobody holds, and a wrong cluster secret.
func forgedStamps(t testing.TB) http.Header {
	t.Helper()
	now := fmt.Sprint(time.Now().Unix())
	return http.Header{
		headerCoordination:  {now + "." + strings.Repeat("0", macHexLen)},
		headerNodeID:        {unheldPeer(t)},
		headerNodeStamp:     {now + "." + strings.Repeat("ab", sigHexLen/2)},
		headerClusterSecret: {notTheSecret},
	}
}

// curlHeaders renders h as curl -H arguments.
func curlHeaders(h http.Header) []string {
	var out []string
	for k, vs := range h {
		for _, v := range vs {
			out = append(out, k+": "+v)
		}
	}
	return out
}

// is4xx reports a client-error status.
func is4xx(status int) bool { return status >= 400 && status < 500 }

// otherNode is a core node that is not n.
func otherNode(t testing.TB, f *fleet.Fleet, n fleet.Node) fleet.Node {
	t.Helper()
	for _, o := range f.State.Nodes {
		if o.Name != n.Name {
			return o
		}
	}
	t.Fatalf("the run has no core node besides %s", n.Name)
	return fleet.Node{}
}

// requireConverged fails unless, after the test's probes, the cluster is
// still converged with every core node, one leader named by all: nothing a
// refused request did moved it.
func requireConverged(t testing.TB) {
	t.Helper()
	infra.WaitConverged(t, len(harness.Fleet(t).State.Nodes), infra.ConvergeBudget, "the cluster to stay converged after "+t.Name())
}
