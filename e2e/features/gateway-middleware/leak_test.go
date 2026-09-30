//go:build e2e_fleet

package gatewaymiddleware

import (
	"encoding/json"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/features/internal/tenancy"
	"github.com/DeBrosOfficial/network/e2e/harness"
	"github.com/DeBrosOfficial/network/e2e/harness/fleet"
	"github.com/DeBrosOfficial/network/e2e/harness/gw"
	"github.com/DeBrosOfficial/network/e2e/harness/ns"
)

// openPaths are answered to anyone (docs/SECURITY.md "What the open health
// and status endpoints show").
var openPaths = []string{"/health", "/v1/health", "/status", "/v1/status", "/v1/internal/ping"}

// Shapes that identify a node: an IPv4 address, a libp2p peer id (Ed25519
// 12D3Koo… or RSA Qm…), a multiaddr.
var leakShapes = map[string]*regexp.Regexp{
	"IPv4 address": regexp.MustCompile(`\b(?:\d{1,3}\.){3}\d{1,3}\b`),
	"peer id":      regexp.MustCompile(`\b(?:12D3Koo[1-9A-HJ-NP-Za-km-z]{40,}|Qm[1-9A-HJ-NP-Za-km-z]{44})\b`),
	"multiaddr":    regexp.MustCompile(`/(?:ip4|ip6|dns4|dns6)/[^"\s]+/(?:tcp|udp)/\d+`),
}

// leaks lists what in body identifies a node of f: the shapes above, and
// any node's name, public or WireGuard address, or the run id every server
// hostname carries.
func leaks(f *fleet.Fleet, body string) []string {
	var out []string
	for what, re := range leakShapes {
		if m := re.FindString(body); m != "" {
			out = append(out, what+" "+m)
		}
	}
	for _, n := range f.AllNodes() {
		for _, s := range []string{n.PublicIP, n.WGIP, "e2e-" + f.State.RunID + "-" + n.Name} {
			if s != "" && strings.Contains(body, s) {
				out = append(out, "node detail "+s)
			}
		}
	}
	for _, s := range []string{"10.0.0.", "localhost", "wg0"} {
		if strings.Contains(strings.ToLower(body), s) {
			out = append(out, "internal detail "+s)
		}
	}
	return out
}

// TestOpenEndpoints_nameNoNode: every open endpoint, through every node and
// on a namespace host, answers without an address, peer id, hostname or
// multiaddr of any node — anonymously they show status only
// (docs/SECURITY.md "What the open health and status endpoints show";
// docs/API_SURFACE.md "Health and version").
func TestOpenEndpoints_nameNoNode(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	n := tenancy.Namespace(t, f, ns.Options{})
	clients := map[string]*gw.Client{"public name": harness.GW(t), "namespace host": n.Client.WithBase(n.URL)}
	for _, node := range f.State.Nodes {
		clients[node.Name] = harness.GW(t).PinTo(node.PublicIP)
	}
	for where, c := range clients {
		for _, path := range openPaths {
			if where == "namespace host" && path == "/v1/internal/ping" {
				continue // only the index gateway serves the ping (docs/MONITORING.md "ring monitor")
			}
			resp := c.MustSend(t, gw.Req{Path: path, Header: http.Header{"Accept": {"application/json"}}})
			if resp.Status != http.StatusOK {
				t.Errorf("%s %s: HTTP %d, want 200 (open endpoint)", where, path, resp.Status)
				continue
			}
			if found := leaks(f, string(resp.Body)); len(found) > 0 {
				t.Errorf("%s %s exposes %v", where, path, found)
			}
		}
	}
}

// TestOpenEndpoints_pingSaysOKAndNothingElse: the peer prober's ping is
// {"status":"ok"} — it used to name the node (docs/API_SURFACE.md
// "/v1/internal/ping"). It is reachable from the internet, like every path
// Caddy proxies; what it shows is the point.
func TestOpenEndpoints_pingSaysOKAndNothingElse(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	for _, node := range f.State.Nodes {
		resp := harness.GW(t).PinTo(node.PublicIP).MustSend(t, gw.Req{Path: "/v1/internal/ping"}).Expect(t, http.StatusOK)
		var body map[string]any
		if err := json.Unmarshal(resp.Body, &body); err != nil {
			t.Fatal(err)
		}
		if len(body) != 1 || body["status"] != "ok" {
			t.Errorf("%s: ping answered %s, want exactly {\"status\":\"ok\"}", node.Name, resp.Body)
		}
	}
}

// TestOpenEndpoints_healthChecksCarryStatusOnly: /health's checks map each
// check to its status and nothing else — no latency, error text or port
// (docs/API_SURFACE.md "/health").
func TestOpenEndpoints_healthChecksCarryStatusOnly(t *testing.T) {
	t.Parallel()
	for _, path := range []string{"/health", "/v1/health"} {
		var body struct {
			Checks map[string]json.RawMessage `json:"checks"`
		}
		if err := harness.GW(t).MustSend(t, gw.Req{Path: path}).Expect(t, http.StatusOK).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if len(body.Checks) == 0 {
			t.Errorf("%s reports no checks, so there is nothing to hold to status only", path)
		}
		for name, raw := range body.Checks {
			var check map[string]any
			if json.Unmarshal(raw, &check) == nil {
				keys := make([]string, 0, len(check))
				for k := range check {
					keys = append(keys, k)
				}
				if !slices.Equal(keys, []string{"status"}) {
					t.Errorf("%s check %s carries %v, want status only", path, name, keys)
				}
				continue
			}
			var status string
			if json.Unmarshal(raw, &status) != nil {
				t.Errorf("%s check %s is %s, want a status", path, name, raw)
			}
		}
	}
}
