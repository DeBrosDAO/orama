//go:build e2e_fleet

package docsclaims

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/harness"
)

// The chain's layout on a node (e2e/scripts/chain-deploy.sh SVC_USER,
// BIN_DIR, HOME_DIR, RPC_PORT).
const (
	chainUser = "orama-chain"
	chainBin  = "/usr/lib/orama-global/bin/oramad"
	chainHome = "/var/lib/orama-global/chain"
	chainRPC  = "tcp://127.0.0.1:31001"
)

// TestNamespaceCap_liveDefaultIsTen: a fresh cluster's per-wallet cap is the
// documented default (docs/CLI_REFERENCE.md#orama-cluster "The per-wallet cap
// stays 10 until an operator raises or lowers it").
func TestNamespaceCap_liveDefaultIsTen(t *testing.T) {
	t.Parallel()
	out := harness.CLI(t).MustOK(t, "cluster", "settings", "show").Stdout
	want := "max-namespaces-per-wallet: " + strconv.Itoa(defaultCap)
	if !strings.Contains(out, want) {
		t.Errorf("cluster settings show does not say %q:\n%s", want, out)
	}
}

// emissionInvariants are the verdicts `query emission invariants` answers.
var emissionInvariants = []string{"minted_within_schedule", "supply_matches_minted"}

// TestSupplyInvariant_holdsOnEveryNode: the emission module's invariants,
// the supply rule among them (docs/CHAIN.md "Supply matches minted"), hold
// on every co-hosted validator, asked the way docs/SECURITY_PLAYBOOKS.md and
// the chain deploy script do.
func TestSupplyInvariant_holdsOnEveryNode(t *testing.T) {
	t.Parallel()
	harness.RequireChain(t)
	f := harness.Fleet(t)
	cmd := "runuser -u " + chainUser + " -- " + chainBin + " --home " + chainHome +
		" query emission invariants --node " + chainRPC + " --output json"
	for _, n := range f.State.Nodes {
		out := f.MustExec(t, n, cmd)
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
