//go:build e2e_fleet

package dnstls

import (
	"context"
	"fmt"
	"slices"
	"testing"
	"time"

	"golang.org/x/net/dns/dnsmessage"

	"github.com/DeBrosOfficial/network/e2e/features/internal/edge"
	"github.com/DeBrosOfficial/network/e2e/features/internal/infra"
	"github.com/DeBrosOfficial/network/e2e/features/internal/tenancy"
	"github.com/DeBrosOfficial/network/e2e/harness"
	"github.com/DeBrosOfficial/network/e2e/harness/eventually"
	"github.com/DeBrosOfficial/network/e2e/harness/fleet"
	"github.com/DeBrosOfficial/network/e2e/harness/ns"
)

// webrtcBudget bounds WebRTC provisioning and its TURN records.
const webrtcBudget = 5 * time.Minute

// hosting are the core nodes running the namespace's gateway.
func hosting(t *testing.T, f *fleet.Fleet, name string) []fleet.Node {
	t.Helper()
	var out []fleet.Node
	for _, n := range f.State.Nodes {
		if f.Unit(t, n, tenancy.UnitGateway(name)) == "active" {
			out = append(out, n)
		}
	}
	if len(out) == 0 {
		t.Fatalf("no core node runs %s", tenancy.UnitGateway(name))
	}
	return out
}

// TestNamespaceDNS_hostAndWildcardNameItsNodes: ns-<ns>.<base>, a name under
// its deployment wildcard and a name several labels below it all resolve, on
// every nameserver, to exactly the nodes serving the namespace, with the
// namespace records' 60s TTL (docs/NAMESERVER_SETUP.md "Record Lifecycle";
// core/pkg/namespace/dns_manager.go).
func TestNamespaceDNS_hostAndWildcardNameItsNodes(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	n := tenancy.Namespace(t, f, ns.Options{})
	host := tenancy.NamespaceHost(f, n.Name)
	want := publicIPs(hosting(t, f, n.Name))
	names := []string{host, "app." + host, "a.b.c.app." + host}
	for _, srv := range edge.Nameservers(f) {
		for _, name := range names {
			a := ask(t, srv.PublicIP, name, dnsmessage.TypeA)
			requireAuthoritative(t, srv.Name, name, a)
			requireTTL(t, srv.Name, name, a, dnsmessage.TypeA, edge.NamespaceRecordTTL)
			if got := sorted(a.Values(dnsmessage.TypeA)); !slices.Equal(got, want) {
				t.Errorf("%s: %s -> %v, want the namespace's nodes %v", srv.Name, name, got, want)
			}
		}
	}
}

// TestNamespaceDNS_missingTypeIsNODATANotNXDOMAIN: a name that exists answers
// NOERROR with no records for a type it has none of — AAAA, NS and TXT for
// ns-<ns>.<base>, a name under its wildcard and the apex, MX for the apex — on
// every nameserver, and its A record still resolves afterwards. NXDOMAIN there
// said the name did not exist, so a resolver that asked AAAA or NS (or
// minimised the name) answered "no such host" to the A query for the negative
// TTL (docs/NAMESERVER_SETUP.md "A negative answer"; core/pkg/coredns/rqlite
// plugin.go handleNegative).
func TestNamespaceDNS_missingTypeIsNODATANotNXDOMAIN(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	n := tenancy.Namespace(t, f, ns.Options{})
	host := tenancy.NamespaceHost(f, n.Name)
	want := publicIPs(hosting(t, f, n.Name))
	base := f.State.BaseDomain
	queries := map[string][]dnsmessage.Type{
		host:          {dnsmessage.TypeAAAA, dnsmessage.TypeNS, dnsmessage.TypeTXT},
		"app." + host: {dnsmessage.TypeAAAA, dnsmessage.TypeTXT},
		base:          {dnsmessage.TypeAAAA, dnsmessage.TypeMX, dnsmessage.TypeTXT},
	}
	for _, srv := range edge.Nameservers(f) {
		for name, types := range queries {
			for _, typ := range types {
				requireNoData(t, srv.Name, fmt.Sprintf("%s %s", name, typ), ask(t, srv.PublicIP, name, typ))
			}
		}
		a := ask(t, srv.PublicIP, host, dnsmessage.TypeA)
		requireAuthoritative(t, srv.Name, host, a)
		if got := sorted(a.Values(dnsmessage.TypeA)); !slices.Equal(got, want) {
			t.Errorf("%s: %s -> %v after the NODATA answers, want the namespace's nodes %v", srv.Name, host, got, want)
		}
	}
}

// TestNamespaceDNS_unknownNamespaceFallsToTheBase: a namespace host nobody
// created is answered by the base wildcard — the nameservers — which is why
// the purge never empties a namespace host (docs/NAMESERVER_SETUP.md, the
// gateway-host purge guard). The gateway there does not serve it.
func TestNamespaceDNS_unknownNamespaceFallsToTheBase(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	host := "ns-" + edge.RandomLabel(t, "nobody") + "." + f.State.BaseDomain
	want := publicIPs(edge.Nameservers(f))
	for _, srv := range edge.Nameservers(f) {
		a := ask(t, srv.PublicIP, host, dnsmessage.TypeA)
		if got := sorted(a.Values(dnsmessage.TypeA)); !slices.Equal(got, want) {
			t.Errorf("%s: %s -> %v, want the base wildcard %v", srv.Name, host, got, want)
		}
	}
}

// TestNamespaceDNS_turnHostsFollowWebRTC: with WebRTC enabled the namespace
// gets turn.ns-<ns> (plain TURN) and turn-<ns> (TURNS, covered by the base
// wildcard certificate), both naming exactly the nodes that hold a TURN
// allocation, TTL 60 (docs/NAMESERVER_SETUP.md; core/pkg/namespace
// dns_manager.go CreateTURNRecords). Before, turn.ns-<ns> is only the
// namespace's own wildcard (the walk outward).
func TestNamespaceDNS_turnHostsFollowWebRTC(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	n := tenancy.Namespace(t, f, ns.Options{Via: ns.ViaOperator})
	host := tenancy.NamespaceHost(f, n.Name)
	turnHost, tlsHost := "turn."+host, fmt.Sprintf("turn-%s.%s", n.Name, f.State.BaseDomain)
	srv := edge.Nameservers(f)[0]
	before := sorted(ask(t, srv.PublicIP, turnHost, dnsmessage.TypeA).Values(dnsmessage.TypeA))
	if !slices.Equal(before, publicIPs(hosting(t, f, n.Name))) {
		t.Fatalf("before WebRTC, %s -> %v, want the namespace wildcard", turnHost, before)
	}
	enableWebRTC(t, n)
	relays := turnRows(t, f, n.Name, tlsHost)
	for _, ip := range relays {
		node, ok := f.Lookup(ip)
		if !ok {
			t.Fatalf("%s names %s, which is no node of the run", tlsHost, ip)
		}
		if !infra.HostRunsTURN(t, f, node) {
			t.Errorf("%s names %s, which holds no TURN allocation (%s missing)", tlsHost, node.Name, infra.TURNConfigPath)
		}
	}
	eventually.Require(t, time.Second, edge.PluginCacheTTL+propagationSlack, "every nameserver to serve the TURN records", func() (bool, error) {
		for _, s := range edge.Nameservers(f) {
			for _, name := range []string{turnHost, tlsHost} {
				a, err := edge.Query(t.Context(), "udp", s.PublicIP, name, dnsmessage.TypeA)
				if err != nil {
					return false, err
				}
				if got := sorted(a.Values(dnsmessage.TypeA)); !slices.Equal(got, relays) {
					return false, fmt.Errorf("%s: %s -> %v, want the TURN nodes %v", s.Name, name, got, relays)
				}
			}
		}
		return true, nil
	})
	a := ask(t, srv.PublicIP, turnHost, dnsmessage.TypeA)
	requireTTL(t, srv.Name, turnHost, a, dnsmessage.TypeA, edge.NamespaceRecordTTL)
}

// turnRows waits until the registry holds the namespace's TURNS records and
// returns their addresses, sorted: the truth the nameservers must serve.
func turnRows(t *testing.T, f *fleet.Fleet, name, tlsHost string) []string {
	t.Helper()
	var ips []string
	eventually.Require(t, edge.PollEvery, webrtcBudget, "TURN records for "+name+" in the registry", func() (bool, error) {
		q, err := infra.IndexQueryAt(t, f, f.State.Nodes[0], "strong",
			"SELECT value FROM dns_records WHERE fqdn = ? AND record_type = 'A' AND is_active = TRUE", edge.Fqdn(tlsHost))
		if err != nil {
			return false, err
		}
		ips = nil
		for _, row := range q.Values {
			if v, ok := row[0].(string); ok {
				ips = append(ips, v)
			}
		}
		if len(ips) == 0 {
			return false, fmt.Errorf("no active A record for %s yet", tlsHost)
		}
		return true, nil
	})
	return sorted(ips)
}

// enableWebRTC turns WebRTC on for n (created ViaOperator) and off again at
// cleanup (docs/CLI_REFERENCE.md "orama namespace enable").
func enableWebRTC(t *testing.T, n *ns.Namespace) {
	t.Helper()
	n.CLI.MustOK(t, "namespace", "enable", "webrtc", "--namespace", n.Name)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), webrtcBudget)
		defer cancel()
		if res, err := n.CLI.Run(ctx, "namespace", "disable", "webrtc", "--namespace", n.Name); err != nil || res.Exit != 0 {
			t.Errorf("cleanup: disabling WebRTC on %s failed: %v %s", n.Name, err, res.Stderr)
		}
	})
}
