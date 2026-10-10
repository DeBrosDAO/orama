//go:build e2e_fleet

package gatewaymiddlewarechaos

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/e2e/features/internal/edge"
	"github.com/DeBrosOfficial/network/e2e/features/internal/infra"
	"github.com/DeBrosOfficial/network/e2e/features/internal/tenancy"
	"github.com/DeBrosOfficial/network/e2e/harness"
	"github.com/DeBrosOfficial/network/e2e/harness/eventually"
	"github.com/DeBrosOfficial/network/e2e/harness/fleet"
	"github.com/DeBrosOfficial/network/e2e/harness/gw"
	"github.com/DeBrosOfficial/network/e2e/harness/monitor"
	"github.com/DeBrosOfficial/network/e2e/harness/ns"
)

// The namespace proxy's breaker opens after 5 consecutive failures of one
// upstream (core/pkg/gateway/circuit_breaker.go defaultFailureThreshold);
// a failed attempt is answered 503 SERVICE_UNAVAILABLE, retryable.
const (
	breakerThreshold = 5
	// breakerProbes is how many requests the test sends: enough to open the
	// breaker and then prove the traffic moved.
	breakerProbes = breakerThreshold + 10
	// indexGatewayUser is the account the cluster gateway runs as.
	indexGatewayUser = "orama"
)

// TestBreaker_localNamespaceGatewayDownFailsOver: a node's cluster gateway
// proxies a namespace host to its own namespace gateway first. When that one
// cannot be reached (its connections are reset), the failures are answered
// 503 with the coded, retryable envelope and the security headers, and
// after the breaker's threshold every request is served by another member —
// the namespace stays up through one node (core/pkg/gateway/middleware.go
// proxyToNamespaceGateway: "automatic failover when a namespace gateway node
// is down").
func TestBreaker_localNamespaceGatewayDownFailsOver(t *testing.T) {
	f := harness.Fleet(t)
	infra.RequireHealthy(t)
	n := tenancy.Namespace(t, f, ns.Options{})
	node := tenancy.Members(t, f, n.Name)[0]
	edge.CutOff(t, f, node, indexGatewayUser, node.WGIP, namespaceGatewayPort(t, f, n.Name, node))
	c := n.Client.PinTo(node.PublicIP)
	failures, lastFailure := 0, -1
	for i := range breakerProbes {
		resp := tenancy.Post(t, c, "/v1/rqlite/query", tenancy.Owner(n), map[string]any{"sql": "SELECT 1", "args": []any{}})
		switch resp.Status {
		case http.StatusOK:
		case http.StatusServiceUnavailable:
			failures, lastFailure = failures+1, i
			requireUnavailable(t, resp)
		default:
			t.Fatalf("request %d through %s: HTTP %d %.200s", i, node.Name, resp.Status, resp.Body)
		}
	}
	if failures == 0 {
		t.Fatalf("no request failed: %s did not proxy to its own namespace gateway first, or the cut-off did not bite", node.Name)
	}
	if failures > breakerThreshold || lastFailure >= breakerThreshold {
		t.Errorf("%d failures, the last at request %d: the breaker should open after %d and send the rest elsewhere", failures, lastFailure, breakerThreshold)
	}
}

// breakerAlertBudget: the node report is collected every 10s and the snapshot
// cached 5s; a minute covers a hung collection too.
const breakerAlertBudget = 2 * time.Minute

// TestBreaker_oneNamespacesFailingGatewayLeavesAnothersCircuitClosed: the
// namespace proxy keeps one breaker per namespace gateway (namespace and
// node), not one per node. With one namespace's gateway cut off on a node that
// hosts another namespace too, the first opens its own breaker, the second is
// served through that node throughout, and the operator's report names only
// the first (website/src/docs/contributor/architecture-reference.mdx#circuit-breakers).
func TestBreaker_oneNamespacesFailingGatewayLeavesAnothersCircuitClosed(t *testing.T) {
	f := harness.Fleet(t)
	infra.RequireHealthy(t)
	spaces := tenancy.Namespaces(t, f, 2, ns.Options{})
	failing, healthy := spaces[0], spaces[1]
	placed := tenancy.MembersOf(t, f, failing.Name, healthy.Name)
	node, ok := sharedMember(placed[failing.Name], placed[healthy.Name])
	if !ok {
		harness.SkipNotApplicable(t, "needs two namespaces with a member on the same node, which a fleet of three nodes always gives")
	}
	edge.CutOff(t, f, node, indexGatewayUser, node.WGIP, namespaceGatewayPort(t, f, failing.Name, node))
	cf, ch := failing.Client.PinTo(node.PublicIP), healthy.Client.PinTo(node.PublicIP)

	for i := range breakerProbes {
		// The failing namespace may answer 200 (another member took the request) or
		// 503 (this node's circuit); either way its attempts fail on this node.
		tenancy.Post(t, cf, "/v1/rqlite/query", tenancy.Owner(failing), map[string]any{"sql": "SELECT 1", "args": []any{}})
		resp := tenancy.Post(t, ch, "/v1/rqlite/query", tenancy.Owner(healthy), map[string]any{"sql": "SELECT 1", "args": []any{}})
		if resp.Status != http.StatusOK {
			t.Fatalf("request %d for %s through %s: HTTP %d %.200s; %s's failing gateway refused another namespace's request",
				i, healthy.Name, node.Name, resp.Status, resp.Body, failing.Name)
		}
	}

	eventually.Require(t, edge.PollEvery, breakerAlertBudget, "the report to name the failing namespace's open breaker", func() (bool, error) {
		r, err := monitor.Get(t.Context(), harness.CLI(t), f.State.Env)
		if err != nil {
			return false, err
		}
		for _, a := range r.Alerts {
			if a.Node != node.PublicIP || a.Subsystem != "namespace" || !strings.Contains(a.Message, "Circuit breaker") {
				continue
			}
			if strings.Contains(a.Message, healthy.Name) {
				return false, fmt.Errorf("the alert names %s, whose gateway is fine: %s", healthy.Name, a.Message)
			}
			if strings.Contains(a.Message, failing.Name) && strings.Contains(a.Message, node.WGIP) {
				return true, nil
			}
		}
		return false, fmt.Errorf("no circuit-breaker alert for %s on %s: %+v", failing.Name, node.Name, r.Alerts)
	})
}

// sharedMember is a node both member lists contain.
func sharedMember(a, b []fleet.Node) (fleet.Node, bool) {
	for _, x := range a {
		for _, y := range b {
			if x.Name == y.Name {
				return x, true
			}
		}
	}
	return fleet.Node{}, false
}

// requireUnavailable checks a failed attempt's answer: the coded, retryable
// envelope a client can act on, with the security headers (the 5xx class).
func requireUnavailable(t *testing.T, resp *gw.Response) {
	t.Helper()
	var body rpcError
	if err := json.Unmarshal(resp.Body, &body); err != nil || body.OK || body.Error.Code != "SERVICE_UNAVAILABLE" || !body.Error.Retryable {
		t.Errorf("503 body %.300s (%v), want {ok:false, error:{code:SERVICE_UNAVAILABLE, retryable:true}}", resp.Body, err)
	}
	for _, h := range []string{"X-Content-Type-Options", "X-Frame-Options", "Strict-Transport-Security", "Permissions-Policy"} {
		if resp.Header.Get(h) == "" {
			t.Errorf("the 503 lacks %s", h)
		}
	}
}

// namespaceGatewayPort is the port name's gateway listens on at n's
// WireGuard address, read from its YAML's listen_addr only (the file holds
// credentials).
func namespaceGatewayPort(t *testing.T, f *fleet.Fleet, name string, n fleet.Node) int {
	t.Helper()
	out := f.MustExec(t, n, "grep -hE '^listen_addr:' "+tenancy.NamespacesDir+"/"+name+"/configs/gateway-*.yaml").Stdout
	line := strings.Trim(strings.TrimSpace(out), `"'`)
	port, err := strconv.Atoi(strings.Trim(line[strings.LastIndex(line, ":")+1:], `"' `))
	if err != nil || port <= 0 {
		t.Fatalf("%s: cannot read %s's gateway port from %q", n.Name, name, out)
	}
	return port
}
