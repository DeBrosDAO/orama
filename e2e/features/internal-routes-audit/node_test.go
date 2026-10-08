//go:build e2e_fleet

package internalroutesaudit

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/features/internal/edge"
	"github.com/DeBrosOfficial/network/e2e/harness"
	"github.com/DeBrosOfficial/network/e2e/harness/fleet"
)

// Where a node-shell probe is aimed.
const (
	viaLoopback = "loopback"
	viaOverlay  = "overlay"
)

// onNode is what a route owes a caller on a node who holds no node
// credential: spawn and the node's own routes are reachable from the host
// and the overlay and refuse a missing or forged MAC or stamp (401);
// telemetry answers 404 to anything unverified.
func onNode(r route, via string) int {
	switch {
	case r.path == "/v1/internal/telemetry":
		return http.StatusNotFound
	default:
		return http.StatusUnauthorized
	}
}

// target is the route on from's own loopback, or on to's overlay address.
func target(r route, via string, to fleet.Node) string {
	if via == viaLoopback {
		return edge.LocalGateway(r.url())
	}
	return edge.OverlayGateway(to, r.url())
}

// TestInternalRoutes_refusedOnTheNodeWithoutNodeCredentials: from every
// node's shell, over its own loopback and over the overlay to another node,
// every node-to-node route refuses a caller that presents no MAC, stamp or
// secret, and one that presents forged ones — being on the host or on the
// mesh is not a credential (docs/SECURITY.md "Inter-gateway trust").
func TestInternalRoutes_refusedOnTheNodeWithoutNodeCredentials(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	for _, from := range f.State.Nodes {
		to := otherNode(t, f, from)
		for _, r := range routes(t) {
			for _, via := range []string{viaLoopback, viaOverlay} {
				for what, headers := range map[string][]string{"nothing": nil, "forged stamps": curlHeaders(forgedStamps(t))} {
					p := edge.NodeCurl{Method: r.method, URL: target(r, via, to), Headers: headers, Body: r.body}.Run(t, f, from)
					if want := onNode(r, via); p.Status != want {
						t.Errorf("%s -> %s %s over %s with %s: HTTP %d (curl exit %d), want %d: %.200s",
							from.Name, r.method, r.path, via, what, p.Status, p.Exit, want, p.Body)
					}
				}
			}
		}
	}
	requireConverged(t)
}

// oversizePrefix streams an oversized body into curl's stdin, so nothing is
// left on the node and no argument exceeds the kernel's limit.
func oversizePrefix() string {
	return fmt.Sprintf(`head -c %d /dev/zero | tr '\000' x | `, oversizeBody)
}

// TestInternalRoutes_oversizedAndMalformedOnTheNodeAre4xx: from a node's
// shell, over loopback and the overlay, with forged stamps, a body past
// every bound and a malformed one are the caller's mistake (4xx), never a
// server error, and the cluster is unmoved.
func TestInternalRoutes_oversizedAndMalformedOnTheNodeAre4xx(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	from := f.State.Nodes[0]
	to := otherNode(t, f, from)
	headers := append(curlHeaders(forgedStamps(t)), "Content-Type: application/json")
	for _, r := range routes(t) {
		for _, via := range []string{viaLoopback, viaOverlay} {
			bodies := map[string]edge.NodeCurl{
				"oversized": {Prefix: oversizePrefix(), Body: "@-"},
				"malformed": {Body: `{"node_id":`},
			}
			for what, c := range bodies {
				c.Method, c.URL, c.Headers = r.method, target(r, via, to), headers
				if p := c.Run(t, f, from); !is4xx(p.Status) {
					t.Errorf("%s %s over %s with a %s body: HTTP %d (curl exit %d, %s), want 4xx: %.200s",
						r.method, r.path, via, what, p.Status, p.Exit, p.Stderr, p.Body)
				}
			}
		}
	}
	requireConverged(t)
}
