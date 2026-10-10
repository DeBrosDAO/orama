//go:build e2e_fleet

package chainnodenames

import (
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/features/internal/chain"
)

func claimMsg(operator, nodeID, name string) chain.Msg {
	return chain.NewMsg("/orama.nodes.v1.MsgClaimNodeName", map[string]any{"operator": operator, "node_id": nodeID, "name": name})
}

func releaseMsg(operator, nodeID string) chain.Msg {
	return chain.NewMsg("/orama.nodes.v1.MsgReleaseNodeName", map[string]any{"operator": operator, "node_id": nodeID})
}

type namedNode struct {
	Name     string   `json:"name"`
	NodeID   string   `json:"node_id"`
	Operator string   `json:"operator"`
	IPs      []string `json:"ips"`
}

// uniqueName is a valid name no other run or test holds.
func uniqueName(t *testing.T) string { return chain.UniqueID(t, "e2e-") }

// TestNodeNames_claimQueryAndRelease: the operator claims a name for its node, the three queries
// answer it, the name is in the paged list, and a release returns the deposit and frees the name.
func TestNodeNames_claimQueryAndRelease(t *testing.T) {
	t.Parallel()
	c := chain.New(t)
	op := c.FundedValidator(t, chain.OperatorNode, chain.Orama(5))
	c.EnsureOperator(t, op)
	id, _ := c.RegisterTestNode(t, op, []string{chain.RoleStorage}, "ipfs")
	name := uniqueName(t)

	chain.RequireOK(t, "claim a node name", c.Submit(t, op, chain.TxOptions{}, claimMsg(op.Address, id, name)))

	var byName struct {
		Node namedNode `json:"node"`
	}
	c.Query(t, op.Node, &byName, "nodes", "node-by-name", name)
	if byName.Node.NodeID != id || byName.Node.Operator != op.Address || byName.Node.Name != name {
		t.Errorf("node-by-name %s = %+v, want node %s of %s", name, byName.Node, id, op.Address)
	}
	var ofNode struct {
		Name struct {
			Name    string    `json:"name"`
			Deposit chain.Int `json:"deposit"`
		} `json:"name"`
	}
	c.Query(t, op.Node, &ofNode, "nodes", "name-of-node", id)
	if ofNode.Name.Name != name || ofNode.Name.Deposit.IsZero() {
		t.Errorf("name-of-node %s = %+v, want %s with a deposit", id, ofNode.Name, name)
	}
	if !listed(t, c, op, name) {
		t.Errorf("the paged list of node names does not contain %s", name)
	}
	c.RequireInvariants(t, "a node name claim")

	chain.RequireOK(t, "release the node name", c.Submit(t, op, chain.TxOptions{}, releaseMsg(op.Address, id)))
	if out := c.QueryFails(t, op.Node, "nodes", "node-by-name", name); !chain.NotFound(out) {
		t.Errorf("node-by-name after the release: %s", out)
	}
	c.RequireInvariants(t, "a node name release")
}

// listed pages through every claimed name, in name order, and reports whether name is among them.
func listed(t *testing.T, c *chain.Chain, op chain.Key, name string) bool {
	t.Helper()
	key := ""
	for page := 0; page < 50; page++ {
		args := []string{"nodes", "node-names", "--limit", "100"}
		if key != "" {
			args = append(args, "--page-key", key)
		}
		var res struct {
			Nodes      []namedNode `json:"nodes"`
			Pagination struct {
				NextKey string `json:"next_key"`
			} `json:"pagination"`
		}
		c.Query(t, op.Node, &res, args...)
		for _, n := range res.Nodes {
			if n.Name == name {
				return true
			}
		}
		if res.Pagination.NextKey == "" {
			return false
		}
		key = res.Pagination.NextKey
	}
	t.Fatal("the list of node names did not end in 50 pages")
	return false
}

// TestNodeNames_refusals: a reserved, a malformed and a taken name are refused, a node holds one
// name, and a release of a name the node does not hold is refused.
func TestNodeNames_refusals(t *testing.T) {
	t.Parallel()
	c := chain.New(t)
	op := c.FundedValidator(t, chain.OperatorNode, chain.Orama(5))
	c.EnsureOperator(t, op)
	first, _ := c.RegisterTestNode(t, op, []string{chain.RoleStorage}, "ipfs")
	second, _ := c.RegisterTestNode(t, op, []string{chain.RoleStorage}, "ipfs")
	name := uniqueName(t)

	chain.RequireRefused(t, "release a name the node does not hold", c.Submit(t, op, chain.TxOptions{}, releaseMsg(op.Address, first)), "has no name")
	for _, bad := range []string{"seed", "seed1", "www", "ns1", "ab", "Upper-Case", "-lead", "trail-", "a.b", "xn--abc"} {
		r := c.Submit(t, op, chain.TxOptions{}, claimMsg(op.Address, first, bad))
		chain.RequireRefused(t, "claim "+bad, r, strings.ToLower("invalid node name"))
	}
	chain.RequireOK(t, "claim a node name", c.Submit(t, op, chain.TxOptions{}, claimMsg(op.Address, first, name)))
	chain.RequireRefused(t, "claim a taken name", c.Submit(t, op, chain.TxOptions{}, claimMsg(op.Address, second, name)), "already claimed")
	chain.RequireRefused(t, "claim a second name for a node", c.Submit(t, op, chain.TxOptions{}, claimMsg(op.Address, first, uniqueName(t))), "already holds")
	c.CleanupSubmit(t, op, "release the node name", releaseMsg(op.Address, first))
	c.RequireInvariants(t, "refused node name claims")
}

// TestNodeNames_retiringTheNodeFreesTheName: a retired node's name is free again and its
// deposit is back with the operator.
func TestNodeNames_retiringTheNodeFreesTheName(t *testing.T) {
	t.Parallel()
	c := chain.New(t)
	op := c.FundedValidator(t, chain.OperatorNode, chain.Orama(5))
	c.EnsureOperator(t, op)
	id, _ := c.RegisterTestNode(t, op, []string{chain.RoleStorage}, "ipfs")
	name := uniqueName(t)
	chain.RequireOK(t, "claim a node name", c.Submit(t, op, chain.TxOptions{}, claimMsg(op.Address, id, name)))

	chain.RequireOK(t, "retire the node", c.Submit(t, op, chain.TxOptions{}, chain.RetireNodeMsg(op.Address, id)))

	if out := c.QueryFails(t, op.Node, "nodes", "node-by-name", name); !chain.NotFound(out) {
		t.Errorf("node-by-name after the retire: %s", out)
	}
	c.RequireInvariants(t, "a retire that released a node name")
}
