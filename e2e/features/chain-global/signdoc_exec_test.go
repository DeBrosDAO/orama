//go:build e2e_fleet

package chainglobal

import (
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/features/internal/chain"
)

// TestOnchainDocs_clusterRowDocumentsExecute: the documents `orama cluster
// register-onchain` and `retire-onchain` print are transactions the chain
// executes: signed by the operator's key (on its node: the RootWallet agent
// cannot sign orama-tx yet, RootWallet task 2857) the first registers the
// public row with the CLI's domain and endpoint, the second retires it.
func TestOnchainDocs_clusterRowDocumentsExecute(t *testing.T) {
	t.Parallel()
	c := chain.New(t)
	s := newSigner(t, c, chain.OperatorNode)
	c.EnsureOperator(t, s.k)
	id := chain.UniqueID(t, "e2e-cli-")
	t.Cleanup(func() { retireClusterAtCleanup(t, c, s, id) })
	reg := doc(t, c, s, "cluster", "register-onchain", "--operator", s.k.Address, "--id", id,
		"--base-domain", c.F.State.BaseDomain, "--endpoint", c.F.State.GatewayURL)
	chain.RequireOK(t, "the CLI's cluster registration", execute(t, c, s, reg))
	var v struct {
		Cluster struct {
			BaseDomain      string   `json:"base_domain"`
			PublicEndpoints []string `json:"public_endpoints"`
			Status          string   `json:"status"`
		} `json:"cluster"`
	}
	c.Query(t, s.k.Node, &v, "nodes", "cluster", id)
	if v.Cluster.BaseDomain != c.F.State.BaseDomain || len(v.Cluster.PublicEndpoints) != 1 || v.Cluster.Status != "CLUSTER_STATUS_ACTIVE" {
		t.Errorf("cluster row %+v", v.Cluster)
	}
	ret := doc(t, c, s, "cluster", "retire-onchain", "--operator", s.k.Address, "--id", id)
	chain.RequireOK(t, "the CLI's cluster retirement", execute(t, c, s, ret))
	c.Query(t, s.k.Node, &v, "nodes", "cluster", id)
	if v.Cluster.Status != "CLUSTER_STATUS_RETIRED" {
		t.Errorf("cluster %s is %s after the CLI's retire", id, v.Cluster.Status)
	}
	c.RequireInvariants(t, "a CLI-built cluster row")
}

// TestOnchainDocs_nodeDocumentsExecute: `orama global bind` + `register`
// build a MsgRegisterNode whose binding the chain verifies; `capacity`
// declares bytes on it; `bond` is refused only for the operator's empty bank
// balance (the chain decoded it); `retire` retires it.
func TestOnchainDocs_nodeDocumentsExecute(t *testing.T) {
	t.Parallel()
	c := chain.New(t)
	s := newSigner(t, c, chain.OperatorNode)
	c.EnsureOperator(t, s.k)
	hot := c.Validator(t, c.Node(t, 1)).Address
	id := chain.UniqueID(t, "e2e-cli-")
	t.Cleanup(func() { retireNodeAtCleanup(t, c, s, id) })
	reg := doc(t, c, s, "global", "register", "--operator", s.k.Address, "--id", id, "--role", "relay", "--role", "storage",
		"--hot-key", hot, "--binding", edBinding(t, c, s.k.Address), "--endpoint", "relay.example.com:443")
	chain.RequireOK(t, "the CLI's node registration", execute(t, c, s, reg))
	var v struct {
		Node struct {
			HotKey   string `json:"hot_key"`
			Status   string `json:"status"`
			Bindings []any  `json:"bindings"`
		} `json:"node"`
	}
	c.Query(t, s.k.Node, &v, "nodes", "node", id)
	if v.Node.HotKey != hot || v.Node.Status != "NODE_STATUS_REGISTERED" || len(v.Node.Bindings) != 1 {
		t.Errorf("node %+v", v.Node)
	}
	capDoc := doc(t, c, s, "global", "capacity", "--operator", s.k.Address, "--id", id, "--bytes", "1048576")
	chain.RequireOK(t, "the CLI's capacity declaration", execute(t, c, s, capDoc))
	bond := doc(t, c, s, "global", "bond", "--operator", s.k.Address, "--id", id, "--role", "storage", "--amount", "1")
	chain.RequireRefused(t, "the CLI's bond from an empty bank", execute(t, c, s, bond), "insufficient funds")
	ret := doc(t, c, s, "global", "retire", "--operator", s.k.Address, "--id", id)
	chain.RequireOK(t, "the CLI's node retirement", execute(t, c, s, ret))
	c.Query(t, s.k.Node, &v, "nodes", "node", id)
	if v.Node.Status != "NODE_STATUS_RETIRED" {
		t.Errorf("node %s is %s after the CLI's retire", id, v.Node.Status)
	}
	c.RequireInvariants(t, "a CLI-built node")
}

// TestOnchainDocs_storageGrantDocumentsExecute: `orama storage grant` builds
// the deal allowance the chain stores (asked back through the Authorization
// query), `revoke` removes it, and `create` on that grant reaches the
// granter's empty bank balance (the chain decoded the deal the CLI built).
func TestOnchainDocs_storageGrantDocumentsExecute(t *testing.T) {
	t.Parallel()
	c := chain.New(t)
	s := newSigner(t, c, 2)
	grantee := c.Validator(t, c.Node(t, 1)).Address
	grant := doc(t, c, s, "storage", "grant", "--signer", s.k.Address, "--grantee", grantee, "--spend-limit", "50000000",
		"--max-piece-bytes", "2048", "--max-duration-epochs", "5")
	chain.RequireOK(t, "the CLI's grant", execute(t, c, s, grant))
	path, req := "/orama.storage.v1.Query/Authorization", chain.PB{}.Text(1, s.k.Address).Text(2, grantee)
	if a := c.ABCIQuery(t, s.k.Node, path, req); a.Code != 0 {
		t.Fatalf("the CLI's grant is not stored: %d %s", a.Code, a.Log)
	}
	t.Cleanup(func() {
		if c.ABCIQuery(t, s.k.Node, path, req).Code == 0 {
			c.CleanupSubmit(t, s.k, "revoke the CLI's grant", chain.NewMsg("/orama.storage.v1.MsgRevokeDealAuthorization",
				map[string]any{"signer": s.k.Address, "grantee": grantee}))
		}
	})
	revoke := doc(t, c, s, "storage", "revoke", "--signer", s.k.Address, "--grantee", grantee)
	chain.RequireOK(t, "the CLI's revoke", execute(t, c, s, revoke))
	if a := c.ABCIQuery(t, s.k.Node, path, req); a.Code == 0 || !chain.NotFound(a.Log) {
		t.Errorf("the grant is still stored after the CLI's revoke: %d %s", a.Code, a.Log)
	}
	create := doc(t, c, s, "storage", "create", "--signer", s.k.Address, "--class", "public-pin", "--nonce", randomHex(t, 32),
		"--piece", randomHex(t, 32)+":1024", "--price", "1", "--duration-epochs", "2")
	chain.RequireRefused(t, "the CLI's deal from an empty bank", execute(t, c, s, create), "insufficient funds: need")
}

func retireClusterAtCleanup(t *testing.T, c *chain.Chain, s signer, id string) {
	out := c.QueryOut(t, s.k.Node, "nodes", "cluster", id)
	if out.Exit != 0 || strings.Contains(out.Stdout, "CLUSTER_STATUS_RETIRED") {
		return
	}
	c.CleanupSubmit(t, s.k, "retire cluster "+id, chain.NewMsg("/orama.nodes.v1.MsgRetireCluster",
		map[string]any{"operator": s.k.Address, "cluster_id": id}))
}

func retireNodeAtCleanup(t *testing.T, c *chain.Chain, s signer, id string) {
	out := c.QueryOut(t, s.k.Node, "nodes", "node", id)
	if out.Exit != 0 || strings.Contains(out.Stdout, "NODE_STATUS_RETIRED") {
		return
	}
	c.CleanupSubmit(t, s.k, "retire node "+id, chain.RetireNodeMsg(s.k.Address, id))
}
