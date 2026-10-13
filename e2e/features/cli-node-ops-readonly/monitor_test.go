//go:build e2e_fleet

package clinodeopsreadonly

import (
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/features/internal/tenancy"
	"github.com/DeBrosOfficial/network/e2e/harness"
	"github.com/DeBrosOfficial/network/e2e/harness/eventually"
	"github.com/DeBrosOfficial/network/e2e/harness/gw"
	"github.com/DeBrosOfficial/network/e2e/harness/ns"
)

// oneShotViews are the monitor views this package owns (cluster, chain and
// report are bootstrap's), docs/whitepaper/technical-reference/appendices/d-cli-reference.md#orama-monitor.
var oneShotViews = []string{"alerts", "dns", "mesh", "namespaces", "node", "service", "traffic"}

// verdictWords: "Every view starts with the verdict: ✓ All systems operational
// or what is degraded" (docs/whitepaper/technical-reference/appendices/d-cli-reference.md#orama-monitor).
var verdictWords = []string{"operational", "degraded", "critical", "warning"}

// TestMonitorViews_tableStartsWithVerdict: every one-shot view prints, and
// starts with the cluster verdict.
func TestMonitorViews_tableStartsWithVerdict(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	cli := harness.CLI(t)
	for _, v := range oneShotViews {
		res := cli.MustOK(t, "status", v, "--env", f.State.Env)
		first := ""
		for _, l := range strings.Split(res.Stdout, "\n") {
			if first = strings.ToLower(strings.TrimSpace(l)); first != "" {
				break
			}
		}
		if !slices.ContainsFunc(verdictWords, func(w string) bool { return strings.Contains(first, w) }) {
			t.Errorf("monitor %s does not start with the verdict: %q", v, first)
		}
	}
}

// TestMonitorViews_jsonDecodes: --json is honoured by every view and prints
// one JSON document (cmd/monitorcmd newOneShotCmd).
func TestMonitorViews_jsonDecodes(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	cli := harness.CLI(t)
	for _, v := range oneShotViews {
		raw(t, cli.MustOK(t, "status", v, "--env", f.State.Env, "--json"))
	}
}

type meshEntry struct {
	Host      string `json:"host"`
	WgIP      string `json:"wg_ip"`
	PeerCount int    `json:"peer_count"`
	Up        bool   `json:"up"`
}

// TestMonitorMesh_fullMeshOnEveryNode: every node's WireGuard interface is
// up with a peer for each other node (website/src/docs/operator/monitoring.mdx: a fresh cluster
// reports a full WireGuard mesh).
func TestMonitorMesh_fullMeshOnEveryNode(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	var mesh []meshEntry
	decode(t, harness.CLI(t).MustOK(t, "status", "mesh", "--env", f.State.Env, "--json"), &mesh)
	if len(mesh) != len(f.State.Nodes) {
		t.Fatalf("mesh reports %d nodes, the fleet has %d: %+v", len(mesh), len(f.State.Nodes), mesh)
	}
	for _, m := range mesh {
		n, ok := f.Lookup(m.Host)
		if !ok {
			t.Errorf("mesh reports %s, which is not a fleet node", m.Host)
			continue
		}
		if !m.Up || m.PeerCount < len(f.State.Nodes)-1 || m.WgIP != n.WGIP {
			t.Errorf("%s: up=%v peers=%d wg=%s (want up, >= %d peers, %s)", n.Name, m.Up, m.PeerCount, m.WgIP, len(f.State.Nodes)-1, n.WGIP)
		}
	}
}

type dnsEntry struct {
	Host            string `json:"host"`
	CoreDNSActive   bool   `json:"coredns_active"`
	CaddyActive     bool   `json:"caddy_active"`
	SOAResolves     bool   `json:"soa_resolves"`
	NSResolves      bool   `json:"ns_resolves"`
	BaseTLSDaysLeft int    `json:"base_tls_days_left"`
	Error           string `json:"error"`
}

// TestMonitorDNS_nameserversServeAndCertsValid: each nameserver runs CoreDNS
// and Caddy, answers SOA and NS, and its certificate is not about to expire.
func TestMonitorDNS_nameserversServeAndCertsValid(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	var dns []dnsEntry
	decode(t, harness.CLI(t).MustOK(t, "status", "dns", "--env", f.State.Env, "--json"), &dns)
	if len(dns) == 0 {
		t.Fatal("monitor dns reports no nameserver")
	}
	for _, d := range dns {
		if !d.CoreDNSActive || !d.CaddyActive || !d.SOAResolves || !d.NSResolves || d.BaseTLSDaysLeft < 1 || d.Error != "" {
			t.Errorf("nameserver %s: %+v", d.Host, d)
		}
	}
}

type nodeEntry struct {
	Host   string         `json:"host"`
	Role   string         `json:"role"`
	Status string         `json:"status"`
	Error  string         `json:"error"`
	Report map[string]any `json:"report"`
}

// TestMonitorNode_filterNarrowsToOneNode: --node takes a public or WireGuard
// IP and shows only that node; an address that is not in the cluster is
// "not found" naming the nodes that are (monitor/source.go FilterNode).
func TestMonitorNode_filterNarrowsToOneNode(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	cli := harness.CLI(t)
	n := f.State.Nodes[len(f.State.Nodes)-1]
	for _, addr := range []string{n.PublicIP, n.WGIP} {
		var nodes []nodeEntry
		decode(t, cli.MustOK(t, "status", "node", "--env", f.State.Env, "--node", addr, "--json"), &nodes)
		if len(nodes) != 1 || nodes[0].Host != n.PublicIP || nodes[0].Report == nil {
			t.Errorf("monitor node --node %s: %+v", addr, nodes)
		}
	}
	res := run(t, cli, "status", "node", "--env", f.State.Env, "--node", documentAddr)
	if res.Exit != exitNotFound || !strings.Contains(output(res), n.PublicIP) {
		t.Errorf("monitor node --node %s: exit %d, want %d naming the real nodes\n%s", documentAddr, res.Exit, exitNotFound, output(res))
	}
}

type serviceEntry struct {
	Host     string            `json:"host"`
	Services map[string]string `json:"services"`
}

// TestMonitorService_noFailedUnit: every node reports its services and none
// is failed on a fresh cluster.
func TestMonitorService_noFailedUnit(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	var svcs []serviceEntry
	decode(t, harness.CLI(t).MustOK(t, "status", "service", "--env", f.State.Env, "--json"), &svcs)
	if len(svcs) != len(f.State.Nodes) {
		t.Fatalf("monitor service reports %d nodes, want %d", len(svcs), len(f.State.Nodes))
	}
	for _, s := range svcs {
		if len(s.Services) == 0 {
			t.Errorf("%s reports no service", s.Host)
		}
		for unit, state := range s.Services {
			if state == "failed" {
				t.Errorf("%s: %s is failed", s.Host, unit)
			}
		}
	}
}

type alertEntry struct {
	Severity string `json:"severity"`
	Node     string `json:"node"`
	Message  string `json:"message"`
}

// TestMonitorAlerts_noCriticalOnFreshCluster: a fresh cluster has no
// critical alert (website/src/docs/operator/monitoring.mdx).
func TestMonitorAlerts_noCriticalOnFreshCluster(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	var alerts []alertEntry
	decode(t, harness.CLI(t).MustOK(t, "status", "alerts", "--env", f.State.Env, "--json"), &alerts)
	for _, a := range alerts {
		if strings.EqualFold(a.Severity, "critical") {
			t.Errorf("critical alert on %s: %s", a.Node, a.Message)
		}
	}
}

// namespaceNodes is how many nodes a tenant namespace is placed on in a
// larger cluster (core/pkg/namespace DefaultRQLiteNodeCount; the e2e module
// cannot import that package).
const namespaceNodes = 3

type namespaceEntry struct {
	Namespace string `json:"namespace"`
	Host      string `json:"host"`
	RQLiteUp  bool   `json:"rqlite_up"`
	OlricUp   bool   `json:"olric_up"`
	GatewayUp bool   `json:"gateway_up"`
}

// TestMonitorNamespaces_newNamespaceHealthyOnEveryNode: a namespace created
// through the API shows up in `monitor namespaces` on every node it is placed on
// (as many as the cluster places, at most namespaceNodes)
// with its RQLite, Olric and gateway up.
func TestMonitorNamespaces_newNamespaceHealthyOnEveryNode(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	n := tenancy.Namespace(t, f, ns.Options{})
	cli := harness.CLI(t)
	eventually.Require(t, pollEvery, telemetryBudget, "namespace healthy on every node", func() (bool, error) {
		var rows []namespaceEntry
		res := run(t, cli, "status", "namespaces", "--env", f.State.Env, "--json")
		if err := jsonOf(res, &rows); err != nil {
			return false, err
		}
		healthy := 0
		for _, r := range rows {
			if r.Namespace == n.Name && r.RQLiteUp && r.OlricUp && r.GatewayUp {
				healthy++
			}
		}
		want := min(len(f.State.Nodes), namespaceNodes)
		if healthy == want {
			return true, nil
		}
		return false, fmt.Errorf("%s healthy on %d of the %d nodes it is placed on", n.Name, healthy, want)
	})
}

type trafficReport struct {
	Totals struct {
		Reporting int   `json:"reporting_gateways"`
		Requests  int64 `json:"requests"`
	} `json:"totals"`
	Namespaces []map[string]any `json:"namespaces"`
}

// TestMonitorTraffic_countsGatewayRequests: traffic is counted by the
// gateways (docs/whitepaper/technical-reference/appendices/d-cli-reference.md#orama-monitor), so requests we send show up
// in the totals of every reporting gateway.
func TestMonitorTraffic_countsGatewayRequests(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	c := harness.GW(t)
	// Not /health or /v1/health: the gateway leaves its own health and
	// telemetry plumbing out of the request metrics (pkg/gateway/traffic.go
	// trafficExcludedPaths), so counting those would never show.
	const burst = 20
	for range burst {
		c.MustSend(t, gw.Req{Method: http.MethodGet, Path: "/v1/status"})
	}
	eventually.Require(t, pollEvery, telemetryBudget, "gateway requests counted", func() (bool, error) {
		var tr trafficReport
		if err := jsonOf(run(t, harness.CLI(t), "status", "traffic", "--env", f.State.Env, "--json"), &tr); err != nil {
			return false, err
		}
		if tr.Totals.Reporting >= 1 && tr.Totals.Requests >= burst {
			return true, nil
		}
		return false, fmt.Errorf("reporting=%d requests=%d", tr.Totals.Reporting, tr.Totals.Requests)
	})
}
