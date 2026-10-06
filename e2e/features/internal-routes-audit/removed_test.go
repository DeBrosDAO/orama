//go:build e2e_fleet

package internalroutesaudit

import (
	"net/http"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/features/internal/edge"
	"github.com/DeBrosOfficial/network/e2e/features/internal/tenancy"
	"github.com/DeBrosOfficial/network/e2e/harness"
	"github.com/DeBrosOfficial/network/e2e/harness/ns"
)

// removedWebRTC are the three internal WebRTC endpoints that were removed
// rather than authenticated (docs/SECURITY.md "Inter-gateway trust"), and
// neverRoute a path under the same prefix that never existed: a removed
// route must be indistinguishable from it.
var (
	removedWebRTC = []string{
		"/v1/internal/namespace/webrtc/enable",
		"/v1/internal/namespace/webrtc/disable",
		"/v1/internal/namespace/webrtc/status",
	}
	neverRoute = "/v1/internal/namespace/e2e-never-a-route"
)

// TestRemovedWebRTCRoutes_answerLikeNoRoute: the removed endpoints answer
// every method exactly as a path that never existed does — 404 to a valid
// credential, the gateway's plain refusal to none — with or without the
// constant that used to authenticate them, from the internet and from a
// node on the overlay. Nothing is ever served.
func TestRemovedWebRTCRoutes_answerLikeNoRoute(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	n := tenancy.Namespace(t, f, ns.Options{})
	c := harness.GW(t)
	legacy := http.Header{headerLegacyAuth: {legacyAuthValue}}
	for _, m := range []string{http.MethodGet, http.MethodPost, http.MethodPut, http.MethodDelete} {
		for what, cred := range map[string]tenancy.Cred{"no credential": {}, "owner session": tenancy.Owner(n)} {
			base := send(t, c, route{path: neverRoute, method: m, body: `{}`}, cred, legacy)
			if cred.Bearer != "" && base.Status != http.StatusNotFound {
				t.Fatalf("%s %s with %s: HTTP %d, want 404 for a path that never existed", m, neverRoute, what, base.Status)
			}
			for _, path := range removedWebRTC {
				resp := send(t, c, route{path: path, method: m, body: `{}`}, cred, legacy)
				if resp.Status != base.Status || resp.ErrorCode() != base.ErrorCode() {
					t.Errorf("%s %s with %s: HTTP %d %q, want what a missing route gets (%d %q): %.200s",
						m, path, what, resp.Status, resp.ErrorCode(), base.Status, base.ErrorCode(), resp.Body)
				}
			}
		}
	}
	from := f.State.Nodes[0]
	to := otherNode(t, f, from)
	header := headerLegacyAuth + ": " + legacyAuthValue
	base := edge.NodeCurl{Method: http.MethodPost, URL: edge.OverlayGateway(to, neverRoute), Headers: []string{header}, Body: `{}`}.Run(t, f, from)
	for _, path := range removedWebRTC {
		p := edge.NodeCurl{Method: http.MethodPost, URL: edge.OverlayGateway(to, path), Headers: []string{header}, Body: `{}`}.Run(t, f, from)
		if p.Status != base.Status || p.Status/100 == 2 {
			t.Errorf("POST %s over the overlay with the old constant: HTTP %d, want %d as a missing route, never 2xx: %.200s",
				path, p.Status, base.Status, p.Body)
		}
	}
}
