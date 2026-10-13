//go:build e2e_fleet

package dnstlschaos

import (
	"slices"
	"sort"
	"testing"

	"golang.org/x/net/dns/dnsmessage"

	"github.com/DeBrosOfficial/network/e2e/features/internal/edge"
)

// ask queries one nameserver over UDP and fails the test when it does not
// answer at all (SERVFAIL and NXDOMAIN are answers).
func ask(t *testing.T, server, name string, typ dnsmessage.Type) *edge.Answer {
	t.Helper()
	a, err := edge.Query(t.Context(), "udp", server, name, typ)
	if err != nil {
		t.Fatalf("nameserver %s did not answer %s: %v", server, name, err)
	}
	return a
}

func sorted(v []string) []string {
	out := slices.Clone(v)
	sort.Strings(out)
	return out
}
