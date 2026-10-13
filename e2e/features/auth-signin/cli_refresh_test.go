//go:build e2e_fleet

package authsignin

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/e2e/harness"
	"github.com/DeBrosOfficial/network/e2e/harness/gw"
	"github.com/DeBrosOfficial/network/e2e/harness/oramacli"
)

// gatewayEnvVar points the CLI at a gateway, over the active environment
// (core/pkg/auth/credentials.go gatewayEnvVars).
const gatewayEnvVar = "ORAMA_GATEWAY_URL"

// parallelCLIs is how many CLI processes renew one session at once.
const parallelCLIs = 6

// faultProxy stands between the CLI and the real gateway and can answer
// /v1/auth/refresh itself with an injected status; everything else, and the
// refresh when no fault is set, goes to the gateway untouched.
type faultProxy struct {
	URL       string
	mu        sync.Mutex
	status    int
	refreshes int
}

func newFaultProxy(t testing.TB, target *gw.Client) *faultProxy {
	t.Helper()
	u, err := url.Parse(target.BaseURL)
	if err != nil {
		t.Fatal(err)
	}
	rp := &httputil.ReverseProxy{Rewrite: func(pr *httputil.ProxyRequest) { pr.SetURL(u) }, Transport: target.HTTP.Transport}
	p := &faultProxy{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == gw.PathRefresh {
			p.mu.Lock()
			p.refreshes++
			st := p.status
			p.mu.Unlock()
			if st != 0 {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(st)
				fmt.Fprintf(w, `{"error":"injected %d"}`, st)
				return
			}
		}
		rp.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)
	p.URL = srv.URL
	return p
}

func (p *faultProxy) inject(status int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.status, p.refreshes = status, 0
}

func (p *faultProxy) refreshCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.refreshes
}

// via returns cli aimed at gatewayURL through the environment variable.
func via(cli *oramacli.Runner, gatewayURL string) *oramacli.Runner {
	cp := *cli
	cp.Env = append(append([]string{}, cli.Env...), gatewayEnvVar+"="+gatewayURL)
	return &cp
}

// cloneCredentials stores the credential held for from under to as well, with
// its access token already expired, so the next command must renew it.
func cloneCredentials(t testing.TB, cli *oramacli.Runner, from, to string) {
	t.Helper()
	store := readStore(t, cli)
	gws := store["gateways"].(map[string]any)
	entry, ok := gws[from]
	if !ok {
		t.Fatalf("no credential stored for %s", from)
	}
	raw, err := json.Marshal(entry)
	if err != nil {
		t.Fatal(err)
	}
	var cp map[string]any
	if err := json.Unmarshal(raw, &cp); err != nil {
		t.Fatal(err)
	}
	gws[to] = cp
	writeStore(t, cli, store)
	expireAccess(t, cli, to)
}

// expireAccess moves the stored access token's expiry into the past. It is
// the client's own clock state; the tokens are left as the gateway issued them.
func expireAccess(t testing.TB, cli *oramacli.Runner, gatewayURL string) {
	t.Helper()
	store := readStore(t, cli)
	entry, _ := store["gateways"].(map[string]any)[gatewayURL].(map[string]any)
	creds, _ := entry["credentials"].([]any)
	if len(creds) == 0 {
		t.Fatalf("no credential stored for %s", gatewayURL)
	}
	for _, c := range creds {
		c.(map[string]any)["access_token_expires_at"] = time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
	}
	writeStore(t, cli, store)
}

func writeStore(t testing.TB, cli *oramacli.Runner, store map[string]any) {
	t.Helper()
	raw, err := json.MarshalIndent(store, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(credPath(cli), raw, credentialsPerm); err != nil {
		t.Fatalf("failed to write the CLI's credential file: %v", err)
	}
}

func whoamiExit(t testing.TB, cli *oramacli.Runner) oramacli.Result {
	t.Helper()
	res, err := cli.For(t).Run(t.Context(), "auth", "whoami")
	if err != nil {
		t.Fatal(err)
	}
	return res
}

// TestAuthRefresh_cliKeepsSessionOnTransientFailure: a 5xx, a 429, a 400 or an
// unreachable gateway fails only that attempt and leaves the stored refresh
// token untouched; the next attempt renews it (docs/whitepaper/technical-reference/vol1/13-identity.md#the-command-line-client).
func TestAuthRefresh_cliKeepsSessionOnTransientFailure(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	cli := loggedIn(t)
	proxy := newFaultProxy(t, harness.GW(t))
	cloneCredentials(t, cli, f.State.GatewayURL, proxy.URL)
	before := readCreds(t, cli, proxy.URL).RefreshToken
	through := via(cli, proxy.URL)
	for _, st := range []int{http.StatusServiceUnavailable, http.StatusInternalServerError, http.StatusBadGateway,
		http.StatusTooManyRequests, http.StatusBadRequest} {
		proxy.inject(st)
		if res := whoamiExit(t, through); res.Exit == 0 {
			t.Errorf("injected %d: whoami succeeded without a renewal", st)
		}
		if proxy.refreshCount() == 0 {
			t.Errorf("injected %d: the CLI never tried to refresh an expired token", st)
		}
		if got := readCreds(t, cli, proxy.URL).RefreshToken; got != before {
			t.Fatalf("injected %d: the stored refresh token changed (kept %v): a transient failure must not end the session", st, got != "")
		}
	}
	closed := unusedLocalURL(t)
	cloneCredentials(t, cli, proxy.URL, closed)
	if res := whoamiExit(t, via(cli, closed)); res.Exit == 0 || readCreds(t, cli, closed).RefreshToken != before {
		t.Errorf("an unreachable gateway: exit %d, refresh kept %v", res.Exit, readCreds(t, cli, closed).RefreshToken == before)
	}
	proxy.inject(0)
	if res := whoamiExit(t, through); res.Exit != 0 {
		t.Fatalf("after the faults cleared the session did not renew: exit %d %s", res.Exit, res.Stderr)
	}
	if after := readCreds(t, cli, proxy.URL); after.RefreshToken == before || time.Until(after.AccessTokenExpiresAt) <= 0 {
		t.Fatal("the renewal did not store the rotated refresh token and a live access token")
	}
}

// TestAuthRefresh_cliEndsSessionOnRefusal: only the gateway refusing the
// refresh token — 401 or 403 — ends the stored session and asks for
// `orama auth login` (docs/whitepaper/technical-reference/vol1/13-identity.md#the-command-line-client).
func TestAuthRefresh_cliEndsSessionOnRefusal(t *testing.T) {
	t.Parallel()
	for _, st := range []int{http.StatusUnauthorized, http.StatusForbidden} {
		t.Run(fmt.Sprintf("injected %d", st), func(t *testing.T) {
			t.Parallel()
			f := harness.Fleet(t)
			cli := loggedIn(t)
			proxy := newFaultProxy(t, harness.GW(t))
			cloneCredentials(t, cli, f.State.GatewayURL, proxy.URL)
			proxy.inject(st)
			expectSessionEnded(t, via(cli, proxy.URL), proxy.URL)
		})
	}
	t.Run("real revocation", func(t *testing.T) {
		t.Parallel()
		f := harness.Fleet(t)
		cli := loggedIn(t)
		held := readCreds(t, cli, f.State.GatewayURL)
		c := harness.GW(t)
		if _, err := c.For(t).Logout(t.Context(), "", held.RefreshToken, held.Namespace, false); err != nil {
			t.Fatal(err)
		}
		expireAccess(t, cli, f.State.GatewayURL)
		expectSessionEnded(t, cli, f.State.GatewayURL)
	})
}

func expectSessionEnded(t testing.TB, cli *oramacli.Runner, gatewayURL string) {
	t.Helper()
	res := whoamiExit(t, cli)
	if res.Exit != exitAuth || !strings.Contains(res.Stdout+res.Stderr, "orama auth login") {
		t.Errorf("a refused refresh: want exit %d naming 'orama auth login', got %d\n%s%s", exitAuth, res.Exit, res.Stdout, res.Stderr)
	}
	if c := readCreds(t, cli, gatewayURL); c != nil && c.RefreshToken != "" {
		t.Error("the refused refresh token is still stored")
	}
}

// TestAuthRefresh_parallelCLIsRenewOnce: several CLI processes finding the
// same expired access token renew it once between them, under the flock on
// credentials.json.lock; without it all but one would present a spent refresh
// token and be refused as a replay (docs/whitepaper/technical-reference/vol1/13-identity.md#the-command-line-client).
func TestAuthRefresh_parallelCLIsRenewOnce(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	cli := loggedIn(t)
	before := readCreds(t, cli, f.State.GatewayURL).RefreshToken
	expireAccess(t, cli, f.State.GatewayURL)
	results := make([]oramacli.Result, parallelCLIs)
	errs := make([]error, parallelCLIs)
	var wg sync.WaitGroup
	for i := range results {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[i], errs[i] = cli.For(t).Run(t.Context(), "auth", "whoami")
		}()
	}
	wg.Wait()
	for i, res := range results {
		if errs[i] != nil || res.Exit != 0 {
			t.Errorf("process %d: exit %d %v\n%s", i, res.Exit, errs[i], res.Stderr)
		}
	}
	after := readCreds(t, cli, f.State.GatewayURL)
	if after.RefreshToken == before || after.RefreshToken == "" {
		t.Fatal("the renewal did not store a rotated refresh token")
	}
	refresh(t, harness.GW(t), after.RefreshToken, after.Namespace).Expect(t, http.StatusOK)
}

// unusedLocalURL is an http URL on a loopback port nothing listens on.
func unusedLocalURL(t testing.TB) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	return "http://" + addr
}
