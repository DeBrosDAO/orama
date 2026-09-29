//go:build e2e_fleet

package anontorchaos

import (
	"context"
	"encoding/json"
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
	pollEvery   = 3 * time.Second
	budget      = 2 * time.Minute
	target      = "https://example.com/"
)

// TestTorDown_failsClosedNodeStaysServing stops node-1's Tor client (the
// cleanup restarts it and waits) and checks, on node-1: /v1/proxy/anon is 503
// and never fetched directly; the tunnel is refused; a function's anon_fetch
// gets status 0 with an error; /v1/health stays 200 with checks.anon_proxy
// "unavailable"; and the node stays in the gateway's DNS answer
// (docs/ARCHITECTURE.md "Health", docs/SERVERLESS.md#http, SECURITY.md).
func TestTorDown_failsClosedNodeStaysServing(t *testing.T) {
	f := harness.Fleet(t)
	n := ns.New(t, f, ns.Options{Via: ns.ViaOperator})
	haveFn := deployFixture(t, n)
	owner := member(t, f, n)
	victim := f.Node(t, "node-1")
	c := harness.GW(t).WithBase(gw.NamespaceURL(f.State, n.Name)).PinTo(victim.PublicIP)
	t.Run("tor stopped", func(t *testing.T) {
		f.StopService(t, victim, torUnit)
		eventually.Require(t, pollEvery, budget, "health to report anon_proxy unavailable", func() (bool, error) {
			r := harness.GW(t).PinTo(victim.PublicIP).MustSend(t, gw.Req{Path: "/v1/health"})
			return r.Status == http.StatusOK && strings.Contains(string(r.Body), `unavailable`), nil
		})
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
		if haveFn {
			anonFetchFailsClosed(t, c)
		}
		stillInDNS(t, f, victim)
	})
	eventually.Require(t, pollEvery, budget, "the proxy back after Tor restarts", func() (bool, error) {
		r := tenancy.Post(t, c, "/v1/proxy/anon", tenancy.Cred{Bearer: owner}, map[string]any{"url": target, "method": "GET"})
		return r.Status == http.StatusOK, nil
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

// deployFixture deploys the serverless fixture as a public function; false
// when tinygo is missing (the anon_fetch check is then not made).
func deployFixture(t *testing.T, n *ns.Namespace) bool {
	t.Helper()
	if _, err := exec.LookPath("tinygo"); err != nil {
		t.Log("tinygo is not on the runner's PATH: anon_fetch with Tor down is not checked")
		return false
	}
	dir := filepath.Join(t.TempDir(), fnName)
	if err := os.MkdirAll(dir, 0o755); err != nil {
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
	return true
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
