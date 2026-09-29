//go:build e2e_fleet

package dnstls

import (
	"slices"
	"sort"
	"strings"
	"testing"

	"golang.org/x/net/dns/dnsmessage"

	"github.com/DeBrosOfficial/network/e2e/features/internal/edge"
	"github.com/DeBrosOfficial/network/e2e/harness/fleet"
)

// ask queries one nameserver over UDP and fails the test when it does not
// answer at all (a refusal or NXDOMAIN is an answer).
func ask(t *testing.T, server, name string, typ dnsmessage.Type) *edge.Answer {
	t.Helper()
	return askOver(t, "udp", server, name, typ)
}

func askOver(t *testing.T, network, server, name string, typ dnsmessage.Type) *edge.Answer {
	t.Helper()
	a, err := edge.Query(t.Context(), network, server, name, typ)
	if err != nil {
		t.Fatalf("nameserver %s did not answer: %v", server, err)
	}
	return a
}

// sorted returns a sorted copy.
func sorted(v []string) []string {
	out := slices.Clone(v)
	sort.Strings(out)
	return out
}

// publicIPs are the public addresses of nodes, sorted.
func publicIPs(nodes []fleet.Node) []string {
	out := make([]string, 0, len(nodes))
	for _, n := range nodes {
		out = append(out, n.PublicIP)
	}
	return sorted(out)
}

// requireAuthoritative fails unless a is a positive, authoritative answer.
func requireAuthoritative(t *testing.T, server, what string, a *edge.Answer) {
	t.Helper()
	if a.RCode != dnsmessage.RCodeSuccess || !a.Authoritative {
		t.Fatalf("%s @%s: rcode %v, authoritative %v; want an authoritative NOERROR", what, server, a.RCode, a.Authoritative)
	}
}

// requireTTL fails unless every answer record of typ carries ttl.
func requireTTL(t *testing.T, server, what string, a *edge.Answer, typ dnsmessage.Type, ttl uint32) {
	t.Helper()
	for _, rr := range a.Answers {
		if rr.Type == typ && rr.TTL != ttl {
			t.Errorf("%s @%s: %s %s has TTL %d, want %d", what, server, rr.Type, rr.Value, rr.TTL, ttl)
		}
	}
}

// under is a fresh name below the base domain.
func under(t *testing.T, base string, labels ...string) string {
	t.Helper()
	return strings.Join(append(labels, edge.RandomLabel(t, "e2e-"), base), ".")
}
