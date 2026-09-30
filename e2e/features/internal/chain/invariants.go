//go:build e2e_fleet

package chain

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/harness/fleet"
)

// InvariantModules are the modules with an Invariants query, the same list
// e2e/scripts/chain-deploy.sh `invariants` checks (docs/SECURITY_PLAYBOOKS.md).
var InvariantModules = []string{"emission", "fees", "houses", "market", "nodes", "power", "relay", "shielded", "storage", "token"}

// requiredTrue are the members a module's Invariants answer must carry as
// true. A module without a hand-written query CLI is printed by autocli's
// amino-JSON encoder, which leaves a false boolean out: for it, an absent
// member is a broken invariant (x/shielded; the others print every member).
var requiredTrue = map[string][]string{"shielded": {"balance_matches", "pools_non_negative", "accumulator_matches"}}

// invariantMark separates the modules' answers in one node's output.
const invariantMark = "__E2E_INVARIANTS__ "

// NodeInvariants runs every module's Invariants query on n in one remote
// command and returns, per module, the boolean members that are false (an
// empty slice: all hold) and the response's detail text.
func (c *Chain) NodeInvariants(t testing.TB, n fleet.Node) map[string][]string {
	t.Helper()
	var script strings.Builder
	for _, m := range InvariantModules {
		fmt.Fprintf(&script, "echo '%s%s'; %s || echo '{\"query_failed\": false}'\n", invariantMark, m,
			OramadCmd("query", m, "invariants", "--node", RPC, "--output", "json"))
	}
	out := c.Run(t, n, QueryBudget, script.String())
	got := map[string][]string{}
	for _, part := range strings.Split(out.Stdout, invariantMark)[1:] {
		m, body, _ := strings.Cut(part, "\n")
		got[strings.TrimSpace(m)] = brokenOf(t, n, m, body)
	}
	for _, m := range InvariantModules {
		if _, ok := got[m]; !ok {
			t.Fatalf("%s: no invariants answer for %s: %s", n.Name, m, c.F.Redact(out.Stderr))
		}
	}
	return got
}

// brokenOf lists the false boolean members of one Invariants response.
func brokenOf(t testing.TB, n fleet.Node, module, body string) []string {
	t.Helper()
	var doc map[string]any
	if err := json.Unmarshal([]byte(body), &doc); err != nil {
		t.Fatalf("%s: %s invariants: %v: %s", n.Name, module, err, body)
	}
	broken := []string{}
	bools := 0
	detail, _ := doc["detail"].(string)
	for _, k := range requiredTrue[module] {
		if v, ok := doc[k].(bool); !ok || !v {
			broken = append(broken, k+" (false or absent: "+detail+")")
		}
	}
	if len(requiredTrue[module]) > 0 {
		sort.Strings(broken)
		return broken
	}
	for k, v := range doc {
		if b, ok := v.(bool); ok {
			bools++
			if !b {
				broken = append(broken, k+" ("+detail+")")
			}
		}
	}
	if bools == 0 {
		t.Fatalf("%s: %s invariants reports no boolean check: %s", n.Name, module, body)
	}
	sort.Strings(broken)
	return broken
}

// RequireInvariants checks every module's invariants on every validator and
// fails the test when one does not hold. Tests call it after each step that
// changes chain state.
func (c *Chain) RequireInvariants(t testing.TB, after string) {
	t.Helper()
	var bad []string
	for _, n := range c.Nodes() {
		for m, broken := range c.NodeInvariants(t, n) {
			if len(broken) > 0 {
				bad = append(bad, fmt.Sprintf("%s %s: %s", n.Name, m, strings.Join(broken, ", ")))
			}
		}
	}
	if len(bad) > 0 {
		sort.Strings(bad)
		t.Fatalf("invariants broken after %s:\n%s", after, strings.Join(bad, "\n"))
	}
}
