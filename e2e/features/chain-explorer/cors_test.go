//go:build e2e_fleet

package chainexplorer

import (
	"net/http"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/harness"
	"github.com/DeBrosOfficial/network/e2e/harness/gw"
)

// explorerOrigin is where the website serves the explorer. It is not under any network's base domain
// (orama.network is the parent of stagenet.orama.network), so the page's reads of a network's gateway
// are cross-origin and the gateway admits the origin by name (core/pkg/gateway/cors_explorer.go).
const explorerOrigin = "https://orama.network"

func sendFrom(t *testing.T, method, path, origin, requestMethod string) *gw.Response {
	t.Helper()
	header := http.Header{"Origin": {origin}, "Accept": {"application/json"}}
	if requestMethod != "" {
		header.Set("Access-Control-Request-Method", requestMethod)
	}
	return harness.GW(t).MustSend(t, gw.Req{Method: method, Path: path, Header: header})
}

// TestExplorerCORS_theExplorerOriginReadsTheChainAndNothingElse: the explorer's own reads (a GET of
// the chain proxy, with no custom header, so no preflight) are answered with the explorer's origin
// echoed, Vary set and no Allow-Credentials, and so is the preflight of a GET; the same origin is
// refused (answered with the base domain, which a browser rejects) on a route that takes a
// credential and on the chain's POST routes, and another origin is refused on the chain read.
func TestExplorerCORS_theExplorerOriginReadsTheChainAndNothingElse(t *testing.T) {
	t.Parallel()
	base := "https://" + harness.Fleet(t).State.BaseDomain

	read := sendFrom(t, http.MethodGet, chainPrefix+"/status", explorerOrigin, "")
	if read.Status != http.StatusOK {
		t.Fatalf("GET %s/status from the explorer: HTTP %d %.200s", chainPrefix, read.Status, read.Body)
	}
	if got := read.Header.Get("Access-Control-Allow-Origin"); got != explorerOrigin {
		t.Errorf("GET %s/status: Allow-Origin %q, want the explorer's origin", chainPrefix, got)
	}
	if read.Header.Get("Vary") == "" || read.Header.Get("Access-Control-Allow-Credentials") != "" {
		t.Errorf("GET %s/status: Vary %q, Allow-Credentials %q; want Vary set and no credentials", chainPrefix, read.Header.Get("Vary"), read.Header.Get("Access-Control-Allow-Credentials"))
	}

	preflight := sendFrom(t, http.MethodOptions, chainPrefix+"/index/txs", explorerOrigin, http.MethodGet)
	if preflight.Status != http.StatusNoContent || preflight.Header.Get("Access-Control-Allow-Origin") != explorerOrigin {
		t.Errorf("preflight of a GET: HTTP %d, Allow-Origin %q, want 204 and the explorer's origin", preflight.Status, preflight.Header.Get("Access-Control-Allow-Origin"))
	}

	refused := []struct{ name, method, path, origin, requestMethod string }{
		{"a route that takes a credential", http.MethodGet, "/v1/auth/whoami", explorerOrigin, ""},
		{"the preflight of the simulate POST", http.MethodOptions, chainPrefix + "/simulate", explorerOrigin, http.MethodPost},
		{"the preflight of the broadcast POST", http.MethodOptions, chainPrefix + "/broadcast", explorerOrigin, http.MethodPost},
		{"another origin on the chain read", http.MethodGet, chainPrefix + "/status", "https://evil.example", ""},
		{"a lookalike of the explorer's origin", http.MethodGet, chainPrefix + "/status", "https://orama.network.evil.example", ""},
	}
	for _, tc := range refused {
		resp := sendFrom(t, tc.method, tc.path, tc.origin, tc.requestMethod)
		if got := resp.Header.Get("Access-Control-Allow-Origin"); got != base {
			t.Errorf("%s: Allow-Origin %q, want the base domain %q (refused)", tc.name, got, base)
		}
	}
}
