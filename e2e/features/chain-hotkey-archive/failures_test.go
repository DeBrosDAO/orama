//go:build e2e_fleet

package chainhotkeyarchive

import (
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/features/internal/chain"
)

// TestNodeFailures_query: x/storage's NodeFailures lists the consecutive
// failure counts of one node's rolled-back work: empty for a node whose work
// applies cleanly (a freshly registered storage node, an id nothing knows), an
// InvalidArgument error without a node id, and the same answer on every
// validator.
func TestNodeFailures_query(t *testing.T) {
	t.Parallel()
	c := chain.New(t)
	k := c.FundedValidator(t, chain.OperatorNode, chain.Orama(1))
	c.EnsureOperator(t, k)
	node := c.RegisterProvenNode(t, k, []string{chain.RoleStorage}, "storage")
	for _, id := range []string{node.ID, chain.UniqueID(t, "e2e-none-")} {
		for _, n := range c.Nodes() {
			a := c.ABCIQuery(t, n, storageQuery+"NodeFailures", chain.PB{}.Text(1, id))
			if a.Code != 0 {
				t.Fatalf("%s: NodeFailures(%s): code %d: %s", n.Name, id, a.Code, a.Log)
			}
			if f, err := chain.DecodePB(a.Value); err != nil || len(f[1]) != 0 {
				t.Errorf("%s: NodeFailures(%s) lists failures: %x (%v)", n.Name, id, a.Value, err)
			}
		}
	}
	a := c.ABCIQuery(t, c.Node(t, 0), storageQuery+"NodeFailures", chain.PB{})
	if a.Code == 0 || !strings.Contains(a.Log, "node_id is required") {
		t.Errorf("NodeFailures without a node id: code %d %q, want the node_id refusal", a.Code, a.Log)
	}
	c.RequireInvariants(t, "a storage node's failure query")
}
