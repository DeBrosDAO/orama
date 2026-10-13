//go:build e2e_fleet

package chaincli

import (
	"fmt"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/features/internal/chain"
)

// notFound is what the CLI prints for a key the chain does not have
// (core/pkg/chainread ErrNotFound).
const notFound = "not found on chain"

// TestChainNode_showsARegisteredNode: `orama chain node <id>` prints the
// x/nodes record of a node through --rpc and through the gateway: its
// operator, roles, hot key and status are what the registration wrote; an id
// nothing registered is a failed read that says the key is not on chain.
func TestChainNode_showsARegisteredNode(t *testing.T) {
	t.Parallel()
	c := chain.New(t)
	rpc := rpcURL(t, c)
	k := c.FundedValidator(t, chain.OperatorNode, chain.Orama(1))
	c.EnsureOperator(t, k)
	node := c.RegisterProvenNode(t, k, []string{chain.RoleRelay}, "relay")
	for name, extra := range map[string][]string{"--rpc": {"--rpc", rpc}, "the gateway": nil} {
		var doc any
		runJSON(t, &doc, append([]string{"chain", "node", node.ID}, extra...)...)
		roles, _ := find(doc, "roles")
		if findString(t, doc, "node_id") != node.ID || findString(t, doc, "operator") != k.Address || findString(t, doc, "hot_key") != node.HotKey ||
			findString(t, doc, "status") != "NODE_STATUS_REGISTERED" || fmt.Sprint(roles) != "["+chain.RoleRelay+"]" {
			t.Errorf("%s: chain node %s printed %v", name, node.ID, doc)
		}
	}
	requireRead(t, run(t, "chain", "node", chain.UniqueID(t, "e2e-none-"), "--rpc", rpc), notFound)
	requireRead(t, run(t, "chain", "node", node.ID, "--rpc", deadRPC), "read")
}

// unknownDeal is a deal id no run chain reaches: deals cannot be funded on
// it (the run holds no bank balance), so none exists.
const unknownDeal = "18446744073709551615"

// TestChainDeal_readsX_storage: `orama chain deal <id>` reads x/storage
// through --rpc; a deal id that is not a number is a usage error, and a deal
// that does not exist (the run chain funds none) is a failed read saying the
// key is not on chain.
func TestChainDeal_readsX_storage(t *testing.T) {
	t.Parallel()
	c := chain.New(t)
	rpc := rpcURL(t, c)
	requireRead(t, run(t, "chain", "deal", unknownDeal, "--rpc", rpc), notFound)
	requireRead(t, run(t, "chain", "deal", "1", "--rpc", deadRPC), "read")
	for _, bad := range []string{"abc", "1.5", "18446744073709551616"} {
		requireUsage(t, run(t, "chain", "deal", bad, "--rpc", rpc), "is not a number")
	}
	// After "--" a leading dash is an argument, not a shorthand flag.
	requireUsage(t, run(t, "chain", "deal", "--rpc", rpc, "--", "-1"), "is not a number")
}
