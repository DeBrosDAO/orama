//go:build e2e_fleet

package monitoring

import (
	"fmt"
	"net"
	"slices"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/e2e/features/internal/edge"
	"github.com/DeBrosOfficial/network/e2e/features/internal/infra"
	"github.com/DeBrosOfficial/network/e2e/features/internal/tenancy"
	"github.com/DeBrosOfficial/network/e2e/harness"
	"github.com/DeBrosOfficial/network/e2e/harness/eventually"
	"github.com/DeBrosOfficial/network/e2e/harness/ns"
)

// oneShots are the monitor's one-shot views (website/src/docs/operator/monitoring.mdx
// "Subcommands"; `report` is always JSON and checked on its own).
var oneShots = []string{"alerts", "chain", "cluster", "dns", "mesh", "namespaces", "node", "service", "traffic"}

// TestMonitor_everyViewAsTableAndJSON: every one-shot view exits 0, starts
// its table with the verdict line, and with --json prints one JSON document
// (website/src/docs/operator/monitoring.mdx "Every table starts with the verdict line; every
// subcommand takes --json").
func TestMonitor_everyViewAsTableAndJSON(t *testing.T) {
	t.Parallel()
	cli := harness.CLI(t)
	for _, view := range oneShots {
		res := cli.MustOK(t, monitorArgs(t, view)...)
		if !startsWithVerdict(res.Stdout) {
			t.Errorf("orama monitor %s: the first line is not the verdict:\n%s", view, res.Stdout)
		}
		var v any
		monitorJSON(t, view, &v)
		if v == nil {
			t.Errorf("orama monitor %s --json printed null", view)
		}
	}
	infra.ExpectExit(t, infra.Run(t, cli, "monitor", "--help"), infra.ExitOK, "live", "report", "traffic")
}

// TestMonitor_clusterAndNodeListEveryNode: cluster and node list every core
// node as ok, the node view with its full report.
func TestMonitor_clusterAndNodeListEveryNode(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	var cluster []struct{ Host, Status string }
	monitorJSON(t, "cluster", &cluster)
	var node []struct {
		Host, Status string
		Report       map[string]any
	}
	monitorJSON(t, "node", &node)
	for _, n := range f.State.Nodes {
		if !hasOK(cluster, n.PublicIP) {
			t.Errorf("monitor cluster does not list %s (%s) ok: %+v", n.Name, n.PublicIP, cluster)
		}
		found := false
		for _, e := range node {
			found = found || (e.Host == n.PublicIP && e.Status == nodeStatusOK && e.Report["hostname"] != nil)
		}
		if !found {
			t.Errorf("monitor node has no full report for %s", n.Name)
		}
	}
}

func hasOK(rows []struct{ Host, Status string }, host string) bool {
	for _, r := range rows {
		if r.Host == host && r.Status == nodeStatusOK {
			return true
		}
	}
	return false
}

// TestMonitor_dnsViewEveryNameserverHealthy: the dns view lists exactly the
// nameservers, each with CoreDNS and Caddy active, SOA, NS and wildcard
// resolving locally and both certificates well within validity
// (website/src/docs/operator/monitoring.mdx "dns"; alert threshold 14 days).
func TestMonitor_dnsViewEveryNameserverHealthy(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	var rows []struct {
		Host     string `json:"host"`
		Coredns  bool   `json:"coredns_active"`
		Caddy    bool   `json:"caddy_active"`
		SOA      bool   `json:"soa_resolves"`
		NS       bool   `json:"ns_resolves"`
		Wild     bool   `json:"wildcard_resolves"`
		BaseDays int    `json:"base_tls_days_left"`
		WildDays int    `json:"wild_tls_days_left"`
	}
	monitorJSON(t, "dns", &rows)
	if len(rows) != len(edge.Nameservers(f)) {
		t.Fatalf("dns view lists %d nameservers, want %d", len(rows), len(edge.Nameservers(f)))
	}
	for _, r := range rows {
		if !r.Coredns || !r.Caddy || !r.SOA || !r.NS || !r.Wild || r.BaseDays < tlsWarnDays || r.WildDays < tlsWarnDays {
			t.Errorf("%s: %+v", r.Host, r)
		}
	}
}

// tlsWarnDays is where the monitor starts warning about a certificate.
const tlsWarnDays = 14

// TestMonitor_meshViewFullAndOverlayOnly: every node's wg0 is up on 51820
// with N-1 peers, each peer a single /32 inside 10.0.0.0/24 — only overlay
// peers (website/src/docs/operator/monitoring.mdx "mesh"; docs/whitepaper/technical-reference/vol1/04-the-node-as-a-supervisor.md "CIDR Validation").
func TestMonitor_meshViewFullAndOverlayOnly(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	var rows []struct {
		Host       string `json:"host"`
		ListenPort int    `json:"listen_port"`
		PeerCount  int    `json:"peer_count"`
		Up         bool   `json:"up"`
		Peers      []struct {
			AllowedIPs string `json:"allowed_ips"`
		} `json:"peers"`
	}
	monitorJSON(t, "mesh", &rows)
	_, mesh, _ := net.ParseCIDR(infra.WireGuardSubnet)
	for _, r := range rows {
		if !r.Up || r.ListenPort != infra.WireGuardPort || r.PeerCount < len(f.State.Nodes)-1 {
			t.Errorf("%s: up %v port %d peers %d, want up on %d with at least %d", r.Host, r.Up, r.ListenPort, r.PeerCount, infra.WireGuardPort, len(f.State.Nodes)-1)
		}
		for _, p := range r.Peers {
			ip, cidr, err := net.ParseCIDR(p.AllowedIPs)
			if ones, _ := cidrSize(cidr); err != nil || ones != 32 || !mesh.Contains(ip) {
				t.Errorf("%s: peer allowed-ips %q, want one /32 inside %s", r.Host, p.AllowedIPs, infra.WireGuardSubnet)
			}
		}
	}
}

func cidrSize(n *net.IPNet) (int, int) {
	if n == nil {
		return 0, 0
	}
	return n.Mask.Size()
}

// TestMonitor_serviceViewNodeActive: the service matrix has every node's
// orama-node active.
func TestMonitor_serviceViewNodeActive(t *testing.T) {
	t.Parallel()
	var rows []struct {
		Host     string            `json:"host"`
		Services map[string]string `json:"services"`
	}
	monitorJSON(t, "service", &rows)
	if len(rows) != len(harness.Fleet(t).State.Nodes) {
		t.Errorf("service view has %d hosts", len(rows))
	}
	for _, r := range rows {
		if r.Services["orama-node"] != "active" {
			t.Errorf("%s: orama-node is %q", r.Host, r.Services["orama-node"])
		}
	}
}

// TestMonitor_alertsViewShape: every alert names a known severity, a
// subsystem and a node, and none is critical on the healthy cluster.
func TestMonitor_alertsViewShape(t *testing.T) {
	t.Parallel()
	var alerts []struct{ Severity, Subsystem, Node, Message string }
	monitorJSON(t, "alerts", &alerts)
	for _, a := range alerts {
		if !slices.Contains([]string{severityCritical, severityWarning, severityInfo}, a.Severity) || a.Subsystem == "" || a.Node == "" || a.Message == "" {
			t.Errorf("malformed alert %+v", a)
		}
		if a.Severity == severityCritical {
			t.Errorf("critical alert on the healthy cluster: %+v", a)
		}
	}
}

// TestMonitor_namespacesViewShowsANewNamespace: the namespaces view lists a
// namespace on every node running it, with its rqlite, Olric and gateway up
// (website/src/docs/operator/monitoring.mdx "namespaces"; the probes run every 30s).
func TestMonitor_namespacesViewShowsANewNamespace(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	n := tenancy.Namespace(t, f, ns.Options{})
	eventually.Require(t, edge.PollEvery, 3*time.Minute, n.Name+" healthy in monitor namespaces", func() (bool, error) {
		var rows []struct {
			Namespace string `json:"namespace"`
			Host      string `json:"host"`
			RQLiteUp  bool   `json:"rqlite_up"`
			OlricUp   bool   `json:"olric_up"`
			GatewayUp bool   `json:"gateway_up"`
		}
		monitorJSON(t, "namespaces", &rows)
		up := 0
		for _, r := range rows {
			if r.Namespace != n.Name {
				continue
			}
			if !r.RQLiteUp || !r.OlricUp || !r.GatewayUp {
				return false, fmt.Errorf("%s on %s: %+v", n.Name, r.Host, r)
			}
			up++
		}
		if up == 0 {
			return false, fmt.Errorf("%s not listed yet", n.Name)
		}
		return true, nil
	})
}
