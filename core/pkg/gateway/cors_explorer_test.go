package gateway

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// corsAnswer runs one request through corsMiddleware on a gateway whose base domain is
// stagenet.orama.network and returns the response and whether the handler behind it ran.
func corsAnswer(t *testing.T, method, path string, header map[string]string) (*httptest.ResponseRecorder, bool) {
	t.Helper()
	g := &Gateway{cfg: &Config{BaseDomain: "stagenet.orama.network"}}
	reached := false
	h := g.corsMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached = true
		w.WriteHeader(http.StatusOK)
	}))
	req := httptest.NewRequest(method, path, nil)
	for k, v := range header {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec, reached
}

const wantBase = "https://stagenet.orama.network"

func TestCORS_explorerOriginIsEchoedOnAChainRead(t *testing.T) {
	for _, path := range []string{"/v1/chain/status", "/v1/chain/index/txs", "/v1/chain/query/cosmos.bank.v1beta1.Query/AllBalances"} {
		rec, reached := corsAnswer(t, http.MethodGet, path, map[string]string{"Origin": publicExplorerOrigin})
		if !reached {
			t.Errorf("GET %s: the handler was not reached", path)
		}
		if got := rec.Header().Get("Access-Control-Allow-Origin"); got != publicExplorerOrigin {
			t.Errorf("GET %s: Allow-Origin %q, want %q", path, got, publicExplorerOrigin)
		}
		if rec.Header().Get("Vary") != "Origin" {
			t.Errorf("GET %s: Vary %q, want Origin: a shared cache must not serve this answer to another origin", path, rec.Header().Get("Vary"))
		}
		if rec.Header().Get("Access-Control-Allow-Credentials") != "" {
			t.Errorf("GET %s: the answer allows credentials", path)
		}
	}
}

func TestCORS_explorerOriginIsEchoedOnAChainHead(t *testing.T) {
	rec, _ := corsAnswer(t, http.MethodHead, "/v1/chain/status", map[string]string{"Origin": publicExplorerOrigin})
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != publicExplorerOrigin {
		t.Errorf("HEAD: Allow-Origin %q, want %q", got, publicExplorerOrigin)
	}
}

func TestCORS_explorerPreflightOfAChainReadIsAnswered(t *testing.T) {
	rec, reached := corsAnswer(t, http.MethodOptions, "/v1/chain/status", map[string]string{
		"Origin":                         publicExplorerOrigin,
		"Access-Control-Request-Method":  http.MethodGet,
		"Access-Control-Request-Headers": "accept",
	})
	if reached || rec.Code != http.StatusNoContent {
		t.Fatalf("preflight: status %d, handler reached %v, want an empty 204 from the middleware", rec.Code, reached)
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != publicExplorerOrigin {
		t.Errorf("preflight: Allow-Origin %q, want %q", got, publicExplorerOrigin)
	}
}

func TestCORS_explorerOriginIsRefusedOffTheChainReadRoutes(t *testing.T) {
	cases := []struct {
		name, method, path string
		header             map[string]string
	}{
		{"a credentialed route", http.MethodGet, "/v1/auth/whoami", nil},
		{"a namespace route", http.MethodGet, "/v1/rqlite/query", nil},
		{"the chain's simulate POST", http.MethodPost, "/v1/chain/simulate", nil},
		{"the chain's broadcast POST", http.MethodPost, "/v1/chain/broadcast", nil},
		{"the faucet POST", http.MethodPost, "/v1/chain/faucet", nil},
		{"a preflight of the faucet POST", http.MethodOptions, "/v1/chain/faucet", map[string]string{"Access-Control-Request-Method": http.MethodPost}},
		{"a preflight of a DELETE", http.MethodOptions, "/v1/chain/status", map[string]string{"Access-Control-Request-Method": http.MethodDelete}},
		{"a preflight that names no method", http.MethodOptions, "/v1/chain/status", nil},
		{"a path that only looks like the chain's", http.MethodGet, "/v1/chainx/status", nil},
		{"the prefix without its slash", http.MethodGet, "/v1/chain", nil},
		{"a path that climbs out of the proxy", http.MethodGet, "/v1/chain/../auth/whoami", nil},
		{"a path with a dot segment", http.MethodGet, "/v1/chain/./status", nil},
		{"a path with a doubled slash", http.MethodGet, "/v1/chain//status", nil},
		{"a path outside the proxy that holds its name", http.MethodGet, "/v1/auth/v1/chain/status", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			header := map[string]string{"Origin": publicExplorerOrigin}
			for k, v := range tc.header {
				header[k] = v
			}
			rec, _ := corsAnswer(t, tc.method, tc.path, header)
			if got := rec.Header().Get("Access-Control-Allow-Origin"); got != wantBase {
				t.Errorf("%s %s: Allow-Origin %q, want the base domain %q, which a browser refuses", tc.method, tc.path, got, wantBase)
			}
		})
	}
}

func TestCORS_otherOriginsGetNothingNewOnAChainRead(t *testing.T) {
	for _, origin := range []string{
		"https://evil.example",
		"http://orama.network",       // another scheme
		"https://orama.network:8443", // another port
		"https://www.orama.network",  // the redirect alias, not the explorer
		"https://orama.network.evil.example",
		"https://evilorama.network",
		"https://xstagenet.orama.network",
		"null",
	} {
		rec, _ := corsAnswer(t, http.MethodGet, "/v1/chain/status", map[string]string{"Origin": origin})
		if got := rec.Header().Get("Access-Control-Allow-Origin"); got != wantBase {
			t.Errorf("GET from %q: Allow-Origin %q, want the base domain %q", origin, got, wantBase)
		}
	}
}

func TestCORS_baseDomainRuleIsUnchangedOnAChainRead(t *testing.T) {
	for _, origin := range []string{"https://stagenet.orama.network", "https://app.stagenet.orama.network", "http://localhost:5173"} {
		rec, _ := corsAnswer(t, http.MethodGet, "/v1/chain/status", map[string]string{"Origin": origin})
		if got := rec.Header().Get("Access-Control-Allow-Origin"); got != origin {
			t.Errorf("GET from %q: Allow-Origin %q, want it echoed", origin, got)
		}
	}
}

func TestCORS_noOriginOnAChainReadGetsTheBaseDomain(t *testing.T) {
	rec, _ := corsAnswer(t, http.MethodGet, "/v1/chain/status", nil)
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != wantBase {
		t.Errorf("Allow-Origin %q without an Origin, want %q", got, wantBase)
	}
}
