//go:build e2e_fleet

package internalroutesaudit

import (
	"net/http"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/features/internal/tenancy"
	"github.com/DeBrosOfficial/network/e2e/harness"
	"github.com/DeBrosOfficial/network/e2e/harness/gw"
	"github.com/DeBrosOfficial/network/e2e/harness/ns"
)

// scopeAdmin is the widest key a namespace can mint (core/pkg/gateway/auth/scopes.go).
const scopeAdmin = "admin"

// spoofed is everything a caller on the internet can claim about where it
// is and who vouched for it: an overlay source in every forwarding header,
// the internal-auth hop headers, the removed coordination constant, and
// forged stamps of every kind (docs/SECURITY.md "Inter-gateway trust").
func spoofed(t testing.TB, namespace string) http.Header {
	t.Helper()
	h := forgedStamps(t)
	for k, v := range map[string]string{
		"X-Forwarded-For": "10.0.0.1", "X-Real-Ip": "10.0.0.1", "Forwarded": "for=10.0.0.1",
		"X-Internal-Auth-Validated": "true", "X-Internal-Auth-Namespace": namespace,
		"X-Internal-Auth-Scopes": scopeAdmin, headerLegacyAuth: legacyAuthValue,
	} {
		h.Set(k, v)
	}
	return h
}

// send sends r's own method, body and query with who and extra headers.
func send(t testing.TB, c *gw.Client, r route, who tenancy.Cred, extra http.Header) *gw.Response {
	t.Helper()
	req := gw.Req{Method: r.method, Path: r.path, Query: r.query, Bearer: who.Bearer, APIKey: who.APIKey, Header: http.Header{}}
	if r.body != "" {
		req.Body = []byte(r.body)
		req.Header.Set("Content-Type", "application/json")
	}
	for k, vs := range extra {
		req.Header[k] = vs
	}
	return c.MustSend(t, req)
}

// TestInternalRoutes_refusedFromTheInternet: through every node's public
// name, every node-to-node route answers exactly its refusal — whatever the
// caller presents: nothing, a namespace owner's session, an admin API key,
// or spoofed forwarding, hop and stamp headers. A client credential is not a
// node credential, and no header a client can set makes it one.
func TestInternalRoutes_refusedFromTheInternet(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	n := tenancy.Namespace(t, f, ns.Options{})
	who := map[string]tenancy.Cred{
		"no credential":   {},
		"owner session":   tenancy.Owner(n),
		"admin API key":   {APIKey: tenancy.APIKey(t, n, scopeAdmin)},
		"spoofed headers": {},
	}
	for _, node := range f.State.Nodes {
		c := harness.GW(t).PinTo(node.PublicIP)
		for _, r := range routes(t) {
			for what, cred := range who {
				var extra http.Header
				if what == "spoofed headers" {
					extra = spoofed(t, n.Name)
				}
				if resp := send(t, c, r, cred, extra); resp.Status != r.internet {
					t.Errorf("%s: %s %s with %s: HTTP %d, want %d: %.200s",
						node.Name, r.method, r.path, what, resp.Status, r.internet, resp.Body)
				}
			}
		}
	}
	requireConverged(t)
}

// otherMethods are the methods a route does not serve.
func otherMethods(served string) []string {
	var out []string
	for _, m := range []string{http.MethodGet, http.MethodPost, http.MethodPut, http.MethodDelete, http.MethodPatch} {
		if m != served {
			out = append(out, m)
		}
	}
	return out
}

// TestInternalRoutes_wrongMethodFromTheInternetNever2xxOr5xx: every other
// method on every node-to-node route is refused as the caller's mistake.
func TestInternalRoutes_wrongMethodFromTheInternetNever2xxOr5xx(t *testing.T) {
	t.Parallel()
	c := harness.GW(t)
	for _, r := range routes(t) {
		for _, m := range otherMethods(r.method) {
			wrong := r
			wrong.method = m
			if resp := send(t, c, wrong, tenancy.Cred{}, nil); !is4xx(resp.Status) {
				t.Errorf("%s %s from the internet: HTTP %d, want 4xx: %.200s", m, r.path, resp.Status, resp.Body)
			}
		}
	}
	requireConverged(t)
}

// TestNodeSelfRoutes_internetAnyMethodIs404: a node's own routes refuse
// (404) any caller that is not a process on the host or a node on the
// overlay — whatever the method — so the public cannot even learn they
// exist (docs/SECURITY.md "A node recording itself").
func TestNodeSelfRoutes_internetAnyMethodIs404(t *testing.T) {
	t.Parallel()
	c := harness.GW(t)
	for _, r := range routes(t) {
		if !r.nodeSelf {
			continue
		}
		for _, m := range otherMethods(r.method) {
			wrong := r
			wrong.method = m
			resp := send(t, c, wrong, tenancy.Cred{}, nil)
			switch resp.Status {
			case http.StatusNotFound:
			case http.StatusMethodNotAllowed:
				t.Errorf("PRODUCT BUG: %s %s from the internet: HTTP 405, want 404 — the node API checks the method "+
					"before the off-host 404 (core/pkg/gateway/handlers/nodeapi/handler.go authenticateAgainst), so the "+
					"public learns the route exists, against docs/SECURITY.md \"A node recording itself\": %.200s", m, r.path, resp.Body)
			default:
				t.Errorf("%s %s from the internet: HTTP %d, want 404: %.200s", m, r.path, resp.Status, resp.Body)
			}
		}
	}
}

// TestInternalRoutes_oversizedAndMalformedFromTheInternetAre4xx: a body
// past every bound, and a malformed one, are refused as the caller's
// mistake, never answered with a server error.
func TestInternalRoutes_oversizedAndMalformedFromTheInternetAre4xx(t *testing.T) {
	t.Parallel()
	c := harness.GW(t)
	huge := `{"cluster_secret":"` + strings.Repeat("x", oversizeBody) + `"}`
	for _, r := range routes(t) {
		for what, body := range map[string]string{"oversized": huge, "malformed": `{"node_id":`} {
			bad := r
			bad.body = body
			if resp := send(t, c, bad, tenancy.Cred{}, nil); !is4xx(resp.Status) {
				t.Errorf("%s %s with a %s body from the internet: HTTP %d, want 4xx: %.200s", r.method, r.path, what, resp.Status, resp.Body)
			}
		}
	}
}
