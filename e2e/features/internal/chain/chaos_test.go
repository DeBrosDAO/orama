//go:build e2e_fleet

package chain

import (
	"strings"
	"testing"
)

func TestHasQuorum_moreThanTwoThirds(t *testing.T) {
	for name, tc := range map[string]struct {
		total, part int64
		want        bool
	}{
		"three equal seats lose one: exactly 2/3 is no quorum": {3, 2, false},
		"five equal seats lose one":                            {5, 4, true},
		"six equal seats lose two: exactly 2/3":                {6, 4, false},
		"seven equal seats lose two":                           {7, 5, true},
		"everyone":                                             {3, 3, true},
		"nobody":                                               {3, 0, false},
		"an empty set":                                         {0, 0, false},
	} {
		if got := hasQuorum(tc.total, tc.part); got != tc.want {
			t.Errorf("%s: hasQuorum(%d, %d) = %v, want %v", name, tc.total, tc.part, got, tc.want)
		}
	}
}

func TestAppHashDisagreement_sameHeightOnly(t *testing.T) {
	agree := map[string]Status{"node-1": {Height: 10, AppHash: "AA"}, "node-2": {Height: 10, AppHash: "AA"}}
	if got := AppHashDisagreement(agree); got != "" {
		t.Errorf("two nodes with one hash reported %q", got)
	}
	apart := map[string]Status{"node-1": {Height: 10, AppHash: "AA"}, "node-2": {Height: 11, AppHash: "BB"}}
	if got := AppHashDisagreement(apart); got != "" {
		t.Errorf("nodes at different heights are not comparable, got %q", got)
	}
	forked := map[string]Status{"node-1": {Height: 10, AppHash: "AA"}, "node-2": {Height: 10, AppHash: "BB"}, "node-3": {Height: 9, AppHash: "CC"}}
	got := AppHashDisagreement(forked)
	if !strings.Contains(got, "height 10") || !strings.Contains(got, "node-1") || !strings.Contains(got, "node-2") {
		t.Errorf("a fork at height 10 reported %q", got)
	}
	if got := AppHashDisagreement(nil); got != "" {
		t.Errorf("no node reported %q", got)
	}
}

func TestPartitionRules_everyChainSideAndFamilyTagged(t *testing.T) {
	rules := partitionRules("abc123", "deadbeef")
	if want := len(netTools) * len(netChains) * 2; len(rules) != want {
		t.Fatalf("%d rules, want %d", len(rules), want)
	}
	seen := map[string]bool{}
	for _, r := range rules {
		for _, want := range []string{"-p tcp", "31000", "-j DROP", "--comment 'e2e-abc123-deadbeef'"} {
			if !strings.Contains(r.spec, want) {
				t.Errorf("rule %q lacks %q", r.spec, want)
			}
		}
		f := strings.Fields(r.spec)
		seen[r.tool+" "+f[0]+" "+f[3]] = true
	}
	for _, tool := range netTools {
		for _, chain := range netChains {
			for _, side := range []string{"--dport", "--sport"} {
				if !seen[tool+" "+chain+" "+side] {
					t.Errorf("no rule for %s %s %s", tool, chain, side)
				}
			}
		}
	}
	if !seen["ip6tables -w INPUT --dport"] {
		t.Error("the IPv6 family is not cut")
	}
}

func TestPartitionRule_removeDeletesAllCopiesThenProvesItGone(t *testing.T) {
	got := partitionRule{tool: "ip6tables -w", spec: "INPUT -p tcp --dport 31000 -j DROP"}.remove()
	for _, want := range []string{"while ip6tables -w -C INPUT", "ip6tables -w -D INPUT", "test $? -eq 1"} {
		if !strings.Contains(got, want) {
			t.Errorf("%q lacks %q", got, want)
		}
	}
}

func TestRangeBroadcastScript_numbersFilesFromOne(t *testing.T) {
	l := &TransferLoad{c: chainFor("fleet"), dir: "/tmp/e2e-chain-load", total: 10}
	script := l.rangeBroadcastScript(3, 5)
	for _, want := range []string{"sed -n '3,5p'", "'/tmp/e2e-chain-load'", "broadcast_tx_sync", "127.0.0.1:31001"} {
		if !strings.Contains(script, want) {
			t.Errorf("script lacks %q:\n%s", want, script)
		}
	}
}
