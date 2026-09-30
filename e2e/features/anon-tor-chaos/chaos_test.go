//go:build e2e_fleet

package anontorchaos

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/e2e/features/internal/tenancy"
	"github.com/DeBrosOfficial/network/e2e/harness"
	"github.com/DeBrosOfficial/network/e2e/harness/eventually"
	"github.com/DeBrosOfficial/network/e2e/harness/fleet"
	"github.com/DeBrosOfficial/network/e2e/harness/gw"
	"github.com/DeBrosOfficial/network/e2e/harness/ns"
)

const (
	torUnit     = "orama-namespace-tor@index.service"
	fixtureDir  = "../serverless/testdata/svcfn"
	fnName      = "e2e-anonfetch"
	fixturePerm = 0o644
	fixtureDirP = 0o755
	pollEvery   = 3 * time.Second
	budget      = 2 * time.Minute
	// recoverBudget bounds the proxy's return after Tor restarts: a fresh
	// Tor client bootstraps its circuits before the first exit answers.
	recoverBudget = 5 * time.Minute
	target        = "https://example.com/"
	// anonProxyUnavailable is checks.anon_proxy.status with the SOCKS port
	// down (gateway/status_handlers.go anonProxyCheck; never "error").
	anonProxyUnavailable = "unavailable"
)

// TestTorDown_failsClosedNodeStaysServing stops node-1's Tor client (the
// cleanup restarts it and waits) and checks, on node-1: /v1/proxy/anon is 503
// and never fetched directly; the tunnel is refused; /v1/health stays 200
// with checks.anon_proxy "unavailable"; and the node stays in the gateway's
// DNS answer (docs/ARCHITECTURE.md "Health", SECURITY.md).
func TestTorDown_failsClosedNodeStaysServing(t *testing.T) {
	f := harness.Fleet(t)
	n := ns.New(t, f, ns.Options{Via: ns.ViaOperator})
	owner := member(t, f, n)
	victim := f.Node(t, "node-1")
	c := harness.GW(t).WithBase(gw.NamespaceURL(f.State, n.Name)).PinTo(victim.PublicIP)
	t.Run("tor stopped", func(t *testing.T) {
		stopTor(t, f, victim)
		r := tenancy.Post(t, c, "/v1/proxy/anon", tenancy.Cred{Bearer: owner}, map[string]any{"url": target, "method": "GET"})
		if r.Status != http.StatusServiceUnavailable || strings.Contains(string(r.Body), "Example Domain") {
			t.Errorf("/v1/proxy/anon with Tor down: want 503 and no content, got %d %.200s", r.Status, r.Body)
		}
		conn, resp, err := c.DialWS(t.Context(), "/v1/proxy/tunnel?"+url.Values{"host": {"example.com"}, "port": {"443"}}.Encode(), owner, nil)
		if err == nil {
			conn.Close()
			t.Error("a tunnel opened with Tor down")
		} else if resp == nil || resp.StatusCode != http.StatusServiceUnavailable {
			t.Errorf("tunnel with Tor down: want 503, got %v", err)
		}
		stillInDNS(t, f, victim)
	})
	requireProxyBack(t, c, owner)
}

// TestTorDown_anonFetchFailsClosed: with node-1's Tor client stopped, a
// function's anon_fetch on node-1 gets status 0 with an error, never a
// direct fetch (docs/SERVERLESS.md#http). It needs tinygo to build the
// fixture function.
func TestTorDown_anonFetchFailsClosed(t *testing.T) {
	if _, err := exec.LookPath("tinygo"); err != nil {
		harness.SkipNotApplicable(t, "tinygo is not on the runner's PATH; the anon_fetch fixture function is compiled with it")
	}
	f := harness.Fleet(t)
	n := ns.New(t, f, ns.Options{Via: ns.ViaOperator})
	deployFixture(t, n)
	owner := member(t, f, n)
	victim := f.Node(t, "node-1")
	c := harness.GW(t).WithBase(gw.NamespaceURL(f.State, n.Name)).PinTo(victim.PublicIP)
	t.Run("tor stopped", func(t *testing.T) {
		stopTor(t, f, victim)
		anonFetchFailsClosed(t, c)
	})
	requireProxyBack(t, c, owner)
}

// stopTor stops victim's Tor client until the (sub)test ends and waits for
// its /v1/health to report checks.anon_proxy unavailable, with HTTP 200.
func stopTor(t *testing.T, f *fleet.Fleet, victim fleet.Node) {
	t.Helper()
	f.StopService(t, victim, torUnit)
	eventually.Require(t, pollEvery, budget, "health to report anon_proxy "+anonProxyUnavailable, func() (bool, error) {
		r := harness.GW(t).PinTo(victim.PublicIP).MustSend(t, gw.Req{Path: "/v1/health"})
		var h struct {
			Checks map[string]struct {
				Status string `json:"status"`
			} `json:"checks"`
		}
		if err := json.Unmarshal(r.Body, &h); err != nil {
			return false, fmt.Errorf("health %d is not JSON: %w: %.200s", r.Status, err, r.Body)
		}
		got := h.Checks["anon_proxy"].Status
		if r.Status == http.StatusOK && got == anonProxyUnavailable {
			return true, nil
		}
		return false, fmt.Errorf("health %d, anon_proxy %q", r.Status, got)
	})
}

// requireProxyBack waits for /v1/proxy/anon to fetch again once the
// subtest's cleanup restarted Tor.
func requireProxyBack(t *testing.T, c *gw.Client, owner string) {
	t.Helper()
	eventually.Require(t, pollEvery, recoverBudget, "the proxy back after Tor restarts", func() (bool, error) {
		r := tenancy.Post(t, c, "/v1/proxy/anon", tenancy.Cred{Bearer: owner}, map[string]any{"url": target, "method": "GET"})
		if r.Status == http.StatusOK {
			return true, nil
		}
		return false, fmt.Errorf("HTTP %d", r.Status)
	})
}

func anonFetchFailsClosed(t *testing.T, c *gw.Client) {
	t.Helper()
	r := c.MustSend(t, gw.Req{Method: http.MethodPost, Path: "/v1/functions/" + fnName + "/invoke",
		Header: http.Header{"Content-Type": {"application/json"}}, Body: []byte(`{"op":"anon_fetch","url":"` + target + `"}`)})
	var out struct {
		Result struct {
			Status float64 `json:"status"`
			Error  string  `json:"error"`
		} `json:"result"`
	}
	if err := json.Unmarshal(r.Expect(t, http.StatusOK).Body, &out); err != nil {
		t.Fatal(err)
	}
	if out.Result.Status != 0 || out.Result.Error == "" {
		t.Errorf("anon_fetch with Tor down: %+v, want status 0 with an error", out.Result)
	}
}

func stillInDNS(t *testing.T, f *fleet.Fleet, victim fleet.Node) {
	t.Helper()
	u, err := url.Parse(f.State.GatewayURL)
	if err != nil {
		t.Fatal(err)
	}
	for _, nsNode := range tenancy.Nameservers(f) {
		addrs, err := tenancy.ResolveAt(t.Context(), nsNode.PublicIP, u.Hostname())
		if err != nil || !slices.Contains(addrs, victim.PublicIP) {
			t.Errorf("%s answers %v (%v) for %s: the node without Tor left DNS", nsNode.Name, addrs, err, u.Hostname())
		}
	}
}

// deployFixture deploys the serverless fixture as a public function.
func deployFixture(t *testing.T, n *ns.Namespace) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), fnName)
	if err := os.MkdirAll(dir, fixtureDirP); err != nil {
		t.Fatal(err)
	}
	for _, file := range []string{"function.go", "host.go", "go.mod"} {
		b, err := os.ReadFile(filepath.Join(fixtureDir, file))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, file), b, fixturePerm); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "function.yaml"), []byte("name: "+fnName+"\npublic: true\ntimeout: 90\n"), fixturePerm); err != nil {
		t.Fatal(err)
	}
	n.CLI.MustOK(t, "function", "deploy", dir)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), budget)
		defer cancel()
		if res, err := n.CLI.Run(ctx, "function", "delete", fnName, "--force"); err != nil || res.Exit != 0 {
			t.Errorf("cleanup: failed to delete function %s: %v %s", fnName, err, res.Stderr)
		}
	})
}

// member adds a runtime member (holds proxy) and returns its session token.
func member(t *testing.T, f *fleet.Fleet, n *ns.Namespace) string {
	t.Helper()
	u := gw.NewUser(t, f, gw.LobbyNamespace)
	n.CLI.MustOK(t, "members", "add", u.Wallet.Address(), "--role", "runtime")
	s, err := harness.GW(t).WithBase(gw.NamespaceURL(f.State, n.Name)).For(t).SignIn(t.Context(), u.Wallet, n.Name, nil)
	if err != nil {
		t.Fatal(err)
	}
	return s.AccessToken
}
