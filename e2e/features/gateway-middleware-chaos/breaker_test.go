//go:build e2e_fleet

package gatewaymiddlewarechaos

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/features/internal/edge"
	"github.com/DeBrosOfficial/network/e2e/features/internal/infra"
	"github.com/DeBrosOfficial/network/e2e/features/internal/tenancy"
	"github.com/DeBrosOfficial/network/e2e/harness"
	"github.com/DeBrosOfficial/network/e2e/harness/fleet"
	"github.com/DeBrosOfficial/network/e2e/harness/gw"
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
	node := f.State.Nodes[0]
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
