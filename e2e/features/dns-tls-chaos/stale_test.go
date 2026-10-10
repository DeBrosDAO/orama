//go:build e2e_fleet

package dnstlschaos

import (
	"context"
	"fmt"
	"slices"
	"testing"
	"time"

	"golang.org/x/net/dns/dnsmessage"

	"github.com/DeBrosOfficial/network/e2e/features/internal/edge"
	"github.com/DeBrosOfficial/network/e2e/features/internal/infra"
	"github.com/DeBrosOfficial/network/e2e/harness"
	"github.com/DeBrosOfficial/network/e2e/harness/eventually"
)

// corednsUser is the account CoreDNS runs as (docs/whitepaper/technical-reference/vol1/05-privilege-and-filesystem-trust.md
// "Per-service accounts"): the cut-off matches its connections only.
const corednsUser = "orama-coredns"

// staleBudget: a primed answer is fresh for the plugin's 30s cache, then
// served stale; allow a sweep of slack.
const staleBudget = edge.PluginCacheTTL + 30*time.Second

// TestStale_backendUnreachableServesCachedAnswers: when a nameserver's
// CoreDNS cannot reach its index rqlite, every name it answered before keeps
// resolving — the same addresses, re-served with a 30s TTL so resolvers come
// back soon (a name a wildcard stood in for, for WildcardStaleWindow, 5 minutes) — while a name it never answered, and a name it only knew as
// a negative answer, is SERVFAIL rather than an invented or stale negative. Other
// nameservers are unaffected, and the node recovers its normal TTLs once the
// backend is back (core/pkg/coredns/rqlite plugin.go serveStaleOrFail,
// cache.go StaleWindow/StaleTTL; "DNS is the last thing that should fail when
// the database does"). The backend is cut off for CoreDNS alone with a
// rejecting firewall rule, so rqlite itself and raft are untouched.
func TestStale_backendUnreachableServesCachedAnswers(t *testing.T) {
	f := harness.Fleet(t)
	infra.RequireHealthy(t)
	n := edge.Nameservers(f)[0]
	base := f.State.BaseDomain
	negative := "_acme-challenge." + edge.RandomLabel(t, "neg-") + "." + base
	if a := ask(t, n.PublicIP, negative, dnsmessage.TypeTXT); a.RCode != dnsmessage.RCodeSuccess || len(a.Answers) != 0 || len(a.Authority) != 1 {
		t.Fatalf("%s is not a negative answer (NODATA with the SOA) before the cut: %v %v %v", negative, a.RCode, a.Answers, a.Authority)
	}
	primed := []string{base, edge.RandomLabel(t, "stale-") + "." + base}
	want := map[string][]string{}
	for _, name := range primed {
		want[name] = values(t, n.PublicIP, name)
	}
	t.Cleanup(func() { requireRecovered(t, n.PublicIP, base) })
	edge.CutOff(t, f, n, corednsUser, n.WGIP, infra.IndexRQLiteHTTP)
	eventually.Require(t, 2*time.Second, staleBudget, "stale answers with a 30s TTL", func() (bool, error) {
		for _, name := range primed {
			a := ask(t, n.PublicIP, name, dnsmessage.TypeA)
			if a.RCode != dnsmessage.RCodeSuccess || !slices.Equal(sorted(a.Values(dnsmessage.TypeA)), want[name]) {
				return false, eventually.Stop(fmt.Errorf("%s answered %v %v with the backend cut off, want the cached %v", name, a.RCode, a.Values(dnsmessage.TypeA), want[name]))
			}
			if a.Answers[0].TTL != uint32(edge.StaleTTL.Seconds()) {
				return false, fmt.Errorf("%s still fresh (TTL %d)", name, a.Answers[0].TTL)
			}
		}
		return true, nil
	})
	for name, typ := range map[string]dnsmessage.Type{negative: dnsmessage.TypeTXT, edge.RandomLabel(t, "never-") + "." + base: dnsmessage.TypeA} {
		if a := ask(t, n.PublicIP, name, typ); a.RCode != dnsmessage.RCodeServerFailure {
			t.Errorf("%s with the backend cut off answered %v %v, want SERVFAIL (a negative answer is never served stale)", name, a.RCode, a.Answers)
		}
	}
	for _, other := range edge.Nameservers(f)[1:] {
		if a := ask(t, other.PublicIP, primed[0], dnsmessage.TypeA); a.RCode != dnsmessage.RCodeSuccess || a.Answers[0].TTL == uint32(edge.StaleTTL.Seconds()) {
			t.Errorf("%s is affected by %s's cut-off: %v TTL %v", other.Name, n.Name, a.RCode, a.Answers)
		}
	}
}

func values(t *testing.T, server, name string) []string {
	t.Helper()
	a := ask(t, server, name, dnsmessage.TypeA)
	if a.RCode != dnsmessage.RCodeSuccess || len(a.Values(dnsmessage.TypeA)) == 0 {
		t.Fatalf("%s did not resolve before the test: %v", name, a.RCode)
	}
	return sorted(a.Values(dnsmessage.TypeA))
}

// requireRecovered waits, from a cleanup, until server answers name with the
// record's own TTL again: the backend is back and nothing is served stale.
func requireRecovered(t *testing.T, server, name string) {
	ctx, cancel := context.WithTimeout(context.Background(), staleBudget+time.Minute)
	defer cancel()
	err := eventually.Poll(ctx, 2*time.Second, staleBudget+time.Minute, "fresh answers after the backend is back", func() (bool, error) {
		a, err := edge.Query(ctx, "udp", server, name, dnsmessage.TypeA)
		if err != nil {
			return false, err
		}
		if a.RCode == dnsmessage.RCodeSuccess && len(a.Answers) > 0 && a.Answers[0].TTL == edge.SystemRecordTTL {
			return true, nil
		}
		return false, fmt.Errorf("%v %v", a.RCode, a.Answers)
	})
	if err != nil {
		t.Errorf("after the backend came back: %v", err)
	}
}
