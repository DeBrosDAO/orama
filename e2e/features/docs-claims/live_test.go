//go:build e2e_fleet

package docsclaims

import (
	"encoding/json"
	"regexp"
	"strconv"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/features/internal/chain"
	"github.com/DeBrosOfficial/network/e2e/harness"
)

// TestNamespaceCap_liveDefaultIsTen: the per-wallet cap with no setting row is
// the documented ten (docs/CLI_REFERENCE.md#orama-cluster "The per-wallet cap
// stays 10 until an operator raises or lowers it"), and the live cluster
// reports a cap inside the range an operator may store. The live value itself
// is not asserted to be ten: stage 1 (bootstrap) deliberately raises it for the
// whole run, on every target, so the run's operator can own the live-namespace
// cap's worth of namespaces; the default is read from the code.
func TestNamespaceCap_liveDefaultIsTen(t *testing.T) {
	t.Parallel()
	if v := codeConst(t, operatorPolicy, "DefaultMaxNamespacesPerWallet"); v != strconv.Itoa(defaultCap) {
		t.Fatalf("%s DefaultMaxNamespacesPerWallet is %s; the docs and this check say %d", operatorPolicy, v, defaultCap)
	}
	ceiling, err := strconv.Atoi(codeConst(t, operatorPolicy, "MaxNamespacesPerWalletCeiling"))
	if err != nil {
		t.Fatalf("%s MaxNamespacesPerWalletCeiling is not a number: %v", operatorPolicy, err)
	}
	out := harness.CLI(t).MustOK(t, "cluster", "settings", "show").Stdout
	m := capLine.FindStringSubmatch(out)
	if m == nil {
		t.Fatalf("cluster settings show has no max-namespaces-per-wallet line:\n%s", out)
	}
	if n := atoi(m[1]); n < 1 || n > ceiling {
		t.Errorf("cluster settings show reports a per-wallet cap of %d, outside 1..%d", n, ceiling)
	}
}

// capLine is the per-wallet cap line of `orama cluster settings show`.
var capLine = regexp.MustCompile(`(?m)^max-namespaces-per-wallet: (\d+)$`)

// emissionInvariants are the verdicts `query emission invariants` answers.
var emissionInvariants = []string{"minted_within_schedule", "supply_matches_minted"}

// TestSupplyInvariant_holdsOnEveryNode: the emission module's invariants,
// the supply rule among them (docs/CHAIN.md "Supply matches minted"), hold
// on every co-hosted validator, asked through the chain helpers so the node,
// network namespace and RPC address are the target's own.
func TestSupplyInvariant_holdsOnEveryNode(t *testing.T) {
	t.Parallel()
	c := chain.New(t)
	for _, n := range c.Nodes() {
		out := c.QueryOut(t, n, "emission", "invariants")
		if out.Exit != 0 {
			t.Fatalf("%s: query emission invariants exited %d: %s", n.Name, out.Exit, c.F.Redact(out.Stderr))
		}
		var inv map[string]any
		if err := json.Unmarshal([]byte(out.Stdout), &inv); err != nil {
			t.Fatalf("%s: emission invariants are not JSON: %v\n%s", n.Name, err, out.Stdout)
		}
		// Named, not counted: a renamed field or a false left out of the JSON
		// (proto3 omits it) must fail, not shrink the set silently
		// (chain/proto/orama/emission/v1/query.proto QueryInvariantsResponse).
		for _, name := range emissionInvariants {
			if ok, isBool := inv[name].(bool); !isBool || !ok {
				t.Errorf("%s: emission invariant %s is %v, want true: %s", n.Name, name, inv[name], out.Stdout)
			}
		}
	}
}
