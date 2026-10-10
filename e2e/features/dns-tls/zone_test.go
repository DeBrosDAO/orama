//go:build e2e_fleet

package dnstls

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"golang.org/x/net/dns/dnsmessage"

	"github.com/DeBrosOfficial/network/e2e/features/internal/edge"
	"github.com/DeBrosOfficial/network/e2e/features/internal/tenancy"
	"github.com/DeBrosOfficial/network/e2e/harness"
	"github.com/DeBrosOfficial/network/e2e/harness/ns"
)

// TestZone_soaAndNSFromEveryNameserver: every nameserver answers the base
// zone's SOA and NS itself (authoritative), the NS set is exactly one nsN
// name per nameserver, and the SOA names the lowest glued slot as primary
// (website/src/docs/operator/nameserver.mdx "Nameserver slots"; TTL 300 from
// core/pkg/node/dns_nameservers.go).
func TestZone_soaAndNSFromEveryNameserver(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	base := f.State.BaseDomain
	servers := edge.Nameservers(f)
	for _, n := range servers {
		soa := ask(t, n.PublicIP, base, dnsmessage.TypeSOA)
		requireAuthoritative(t, n.Name, "SOA "+base, soa)
		requireTTL(t, n.Name, "SOA", soa, dnsmessage.TypeSOA, edge.SystemRecordTTL)
		if got := soa.Values(dnsmessage.TypeSOA); len(got) != 1 || !strings.HasPrefix(got[0], "ns1."+edge.Fqdn(base)) {
			t.Errorf("%s: SOA %v, want exactly one naming ns1.%s as primary", n.Name, got, base)
		}
		nsSet := ask(t, n.PublicIP, base, dnsmessage.TypeNS)
		requireAuthoritative(t, n.Name, "NS "+base, nsSet)
		requireTTL(t, n.Name, "NS", nsSet, dnsmessage.TypeNS, edge.SystemRecordTTL)
		var want []string
		for i := range servers {
			want = append(want, fmt.Sprintf("ns%d.%s", i+1, edge.Fqdn(base)))
		}
		if got := sorted(nsSet.Values(dnsmessage.TypeNS)); !slices.Equal(got, sorted(want)) {
			t.Errorf("%s: NS set %v, want one slot per nameserver %v", n.Name, got, want)
		}
	}
}

// TestZone_glueNamesEveryNameserverOnce: each nsN.<base> resolves to exactly
// one nameserver's public address and every nameserver holds one slot
// (website/src/docs/operator/nameserver.mdx "NS records and glue").
func TestZone_glueNamesEveryNameserverOnce(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	base := f.State.BaseDomain
	servers := edge.Nameservers(f)
	for _, asked := range servers {
		var holders []string
		for i := range servers {
			glue := ask(t, asked.PublicIP, fmt.Sprintf("ns%d.%s", i+1, base), dnsmessage.TypeA)
			requireAuthoritative(t, asked.Name, "glue", glue)
			requireTTL(t, asked.Name, "glue", glue, dnsmessage.TypeA, edge.SystemRecordTTL)
			a := glue.Values(dnsmessage.TypeA)
			if len(a) != 1 {
				t.Fatalf("%s: ns%d.%s has %v, want exactly one address", asked.Name, i+1, base, a)
			}
			holders = append(holders, a[0])
		}
		if got := sorted(holders); !slices.Equal(got, publicIPs(servers)) {
			t.Errorf("%s: the slots name %v, want each nameserver once %v", asked.Name, got, publicIPs(servers))
		}
	}
}

// TestZone_apexAndWildcardAreTheNameservers: the base name and a random name
// under it resolve to the nameservers' public addresses, the same set on
// every nameserver, over UDP and TCP (core/pkg/node/dns_registration.go
// ensureBaseDNSRecords: base and *.base are nameserver-only).
func TestZone_apexAndWildcardAreTheNameservers(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	base := f.State.BaseDomain
	want := publicIPs(edge.Nameservers(f))
	random := under(t, base)
	for _, n := range edge.Nameservers(f) {
		for _, network := range []string{"udp", "tcp"} {
			for _, name := range []string{base, random} {
				a := askOver(t, network, n.PublicIP, name, dnsmessage.TypeA)
				requireAuthoritative(t, n.Name, name, a)
				requireTTL(t, n.Name, name, a, dnsmessage.TypeA, edge.SystemRecordTTL)
				if got := sorted(a.Values(dnsmessage.TypeA)); !slices.Equal(got, want) {
					t.Errorf("%s (%s): %s -> %v, want the nameservers %v", n.Name, network, name, got, want)
				}
				for _, rr := range a.Answers {
					if rr.Name != edge.Fqdn(name) {
						t.Errorf("%s: answer owner %q, want the name asked %q (a wildcard answer is synthesised)", n.Name, rr.Name, edge.Fqdn(name))
					}
				}
			}
		}
	}
}

// TestZone_wildcardWalksOutward: a name several labels below the base,
// under no more specific wildcard, falls back to *.<base> — the walk drops
// one label at a time instead of stopping after two (core/pkg/coredns/rqlite
// plugin.go wildcardCandidates, the bug that broke turn.ns-<ns>).
func TestZone_wildcardWalksOutward(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	base := f.State.BaseDomain
	want := publicIPs(edge.Nameservers(f))
	for _, depth := range []int{2, 3, 6} {
		labels := make([]string, depth)
		for i := range labels {
			labels[i] = fmt.Sprintf("l%d", i)
		}
		name := under(t, base, labels...)
		for _, n := range edge.Nameservers(f) {
			a := ask(t, n.PublicIP, name, dnsmessage.TypeA)
			requireAuthoritative(t, n.Name, name, a)
			if got := sorted(a.Values(dnsmessage.TypeA)); !slices.Equal(got, want) {
				t.Errorf("%s: %s (depth %d) -> %v, want the base wildcard %v", n.Name, name, depth, got, want)
			}
		}
	}
}

// TestZone_missingTypeIsNoDataNotNXDOMAIN: a name that is served has no AAAA,
// NS, MX or TXT record, and every nameserver says so with NOERROR, no answer
// and the apex SOA, over UDP and TCP, for the apex, a glue name, a namespace's
// ns-<ns>.<base>, and names only a wildcard covers (the base's, and the
// namespace's), and the namespace's A record still resolves afterwards. NXDOMAIN
// there said the name did not exist, so the resolvers that apply RFC 8020 (or
// minimise the name, RFC 9156) cached the whole name as nonexistent after one
// AAAA or NS query and the next A lookup of a served host failed with "no such
// host" (website/src/docs/operator/nameserver.mdx "A negative answer"; core/pkg/coredns/rqlite
// plugin.go lookup and handleNegative).
func TestZone_missingTypeIsNoDataNotNXDOMAIN(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	n := tenancy.Namespace(t, f, ns.Options{})
	base := f.State.BaseDomain
	host := tenancy.NamespaceHost(f, n.Name)
	want := publicIPs(hosting(t, f, n.Name))
	names := []string{base, "ns1." + base, host, "app." + host, under(t, base), under(t, base, "a", "b")}
	for _, srv := range edge.Nameservers(f) {
		for _, network := range []string{"udp", "tcp"} {
			for _, name := range names {
				for _, typ := range []dnsmessage.Type{dnsmessage.TypeAAAA, dnsmessage.TypeNS, dnsmessage.TypeMX, dnsmessage.TypeTXT} {
					if name == base && (typ == dnsmessage.TypeNS) {
						continue // the apex owns its NS records
					}
					requireNegative(t, srv.Name, fmt.Sprintf("%s %s over %s", typ, name, network), base, askOver(t, network, srv.PublicIP, name, typ))
				}
			}
		}
		a := ask(t, srv.PublicIP, host, dnsmessage.TypeA)
		requireAuthoritative(t, srv.Name, host, a)
		if got := sorted(a.Values(dnsmessage.TypeA)); !slices.Equal(got, want) {
			t.Errorf("%s: %s -> %v after the NODATA answers, want the namespace's nodes %v", srv.Name, host, got, want)
		}
	}
}

// TestZone_caseInsensitive: a mixed-case name is the same name (RFC 4343).
func TestZone_caseInsensitive(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	n := edge.Nameservers(f)[0]
	lower := ask(t, n.PublicIP, f.State.BaseDomain, dnsmessage.TypeA)
	upper := ask(t, n.PublicIP, strings.ToUpper(f.State.BaseDomain), dnsmessage.TypeA)
	if !slices.Equal(sorted(lower.Values(dnsmessage.TypeA)), sorted(upper.Values(dnsmessage.TypeA))) || upper.RCode != dnsmessage.RCodeSuccess {
		t.Fatalf("upper-case base answered %v %v, lower-case %v", upper.RCode, upper.Values(dnsmessage.TypeA), lower.Values(dnsmessage.TypeA))
	}
}

// TestZone_notAnOpenResolver: a name outside the zone is refused to a remote
// client — recursion answers loopback only (website/src/docs/operator/nameserver.mdx
// "Security Considerations": acl allowing 127.0.0.0/8 and ::1).
func TestZone_notAnOpenResolver(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	for _, n := range edge.Nameservers(f) {
		for _, name := range []string{"example.com", "dbrsteting.bid", "com"} {
			a := ask(t, n.PublicIP, name, dnsmessage.TypeA)
			if a.RCode != dnsmessage.RCodeRefused || len(a.Answers) != 0 {
				t.Errorf("%s: %s from the internet answered %v with %d records, want REFUSED and nothing", n.Name, name, a.RCode, len(a.Answers))
			}
		}
	}
}

// TestZone_localRecursionStillWorks: the same listener answers the node's
// own processes (apt, ACME) for outside names, and its own zone from the
// authoritative block rather than a public resolver's cache — the reason the
// two blocks share one listener (website/src/docs/operator/nameserver.mdx).
func TestZone_localRecursionStillWorks(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	for _, n := range edge.Nameservers(f) {
		for _, name := range []string{"example.com", f.State.BaseDomain} {
			a := edge.QueryFromNode(t, f, n, "127.0.0.1", name, uint16(dnsmessage.TypeA))
			if a.RCode != int(dnsmessage.RCodeSuccess) || a.Answers == 0 {
				t.Errorf("%s: loopback query for %s answered rcode %d with %d records, want an answer", n.Name, name, a.RCode, a.Answers)
			}
		}
	}
}
