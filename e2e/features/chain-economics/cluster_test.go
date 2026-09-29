//go:build e2e_fleet

package chaineconomics

import (
	"fmt"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/features/internal/chain"
)

func clusterMsg(typ, operator, id, domain string, endpoints []string, meta string) chain.Msg {
	f := map[string]any{"operator": operator, "cluster_id": id}
	if typ != "MsgRetireCluster" {
		f["base_domain"], f["public_endpoints"], f["metadata_uri"] = domain, endpoints, meta
	}
	return nodeMsg(typ, f)
}

type clusterView struct {
	Cluster struct {
		ClusterID       string    `json:"cluster_id"`
		Operator        string    `json:"operator"`
		BaseDomain      string    `json:"base_domain"`
		PublicEndpoints []string  `json:"public_endpoints"`
		MetadataURI     string    `json:"metadata_uri"`
		Status          string    `json:"status"`
		DepositBytes    chain.Int `json:"deposit_bytes"`
	} `json:"cluster"`
}

// TestNodesCluster_lifecycle: an operator registers a public cluster row (its
// deposit locked from earnings), updates it, and retires it (the deposit
// released); the id can never be registered again, a retired row cannot be
// updated or retired again, and another account can neither update nor
// retire it (docs/CHAIN.md "x/nodes" Cluster). The row names the run's own
// public base domain and gateway, which is what a real cluster publishes.
func TestNodesCluster_lifecycle(t *testing.T) {
	t.Parallel()
	c := chain.New(t)
	k := operator(t, c)
	other := c.FundedValidator(t, 1, chain.Orama(1))
	id := chain.UniqueID(t, "e2e-cluster-")
	domain := c.F.State.BaseDomain
	eps := []string{c.F.State.GatewayURL}
	t.Cleanup(func() { retireClusterAtCleanup(t, c, k, id) })
	chain.RequireOK(t, "register cluster", c.Submit(t, k, chain.TxOptions{}, clusterMsg("MsgRegisterCluster", k.Address, id, domain, eps, "")))
	v := queryCluster(t, c, id)
	if v.Cluster.Operator != k.Address || v.Cluster.BaseDomain != domain || v.Cluster.Status != "CLUSTER_STATUS_ACTIVE" || v.Cluster.DepositBytes.IsZero() {
		t.Errorf("cluster row %+v", v.Cluster)
	}
	dep := deposit(t, c, "nodes/cluster/"+id)
	if dep.Owner != k.Address || dep.Amount.IsZero() {
		t.Errorf("cluster deposit %+v", dep)
	}
	chain.RequireRefused(t, "register the same id", c.Submit(t, k, chain.TxOptions{}, clusterMsg("MsgRegisterCluster", k.Address, id, domain, eps, "")), "already exists")
	meta := "https://" + domain + "/cluster.json"
	chain.RequireOK(t, "update", c.Submit(t, k, chain.TxOptions{}, clusterMsg("MsgUpdateCluster", k.Address, id, domain, eps, meta)))
	if got := queryCluster(t, c, id).Cluster.MetadataURI; got != meta {
		t.Errorf("metadata uri %q after update, want %q", got, meta)
	}
	chain.RequireRefused(t, "update by another account", c.Submit(t, other, chain.TxOptions{}, clusterMsg("MsgUpdateCluster", other.Address, id, domain, eps, "")), "signer is not the operator")
	chain.RequireRefused(t, "retire by another account", c.Submit(t, other, chain.TxOptions{}, clusterMsg("MsgRetireCluster", other.Address, id, "", nil, "")), "signer is not the operator")
	chain.RequireOK(t, "retire", c.Submit(t, k, chain.TxOptions{}, clusterMsg("MsgRetireCluster", k.Address, id, "", nil, "")))
	if st := queryCluster(t, c, id).Cluster.Status; st != "CLUSTER_STATUS_RETIRED" {
		t.Errorf("status after retire %s", st)
	}
	if out := c.QueryFails(t, k.Node, "fees", "deposit", "nodes/cluster/"+id); !chain.NotFound(out) {
		t.Errorf("cluster deposit still readable after retire: %s", out)
	}
	chain.RequireRefused(t, "retire twice", c.Submit(t, k, chain.TxOptions{}, clusterMsg("MsgRetireCluster", k.Address, id, "", nil, "")), "RETIRED")
	chain.RequireRefused(t, "update a retired row", c.Submit(t, k, chain.TxOptions{}, clusterMsg("MsgUpdateCluster", k.Address, id, domain, eps, "")), "RETIRED")
	chain.RequireRefused(t, "re-register a retired id", c.Submit(t, k, chain.TxOptions{}, clusterMsg("MsgRegisterCluster", k.Address, id, domain, eps, "")), "already exists")
	c.RequireInvariants(t, "a cluster row lifecycle")
}

// TestNodesCluster_refusals: the stateless and keeper checks of a cluster
// row: a base domain that is not a lowercase multi-label name, no or
// non-public endpoints, a metadata URI that is not public https, a
// non-operator signer.
func TestNodesCluster_refusals(t *testing.T) {
	t.Parallel()
	c := chain.New(t)
	k := operator(t, c)
	id := chain.UniqueID(t, "e2e-cluster-")
	ep := []string{c.F.State.GatewayURL}
	cases := map[string]struct {
		domain string
		eps    []string
		meta   string
		want   string
	}{
		"uppercase domain":    {"E2E.Example.com", ep, "", "base domain must be lowercase"},
		"single-label domain": {"localhost", ep, "", "at least two labels"},
		"ip as domain":        {"203.0.113.7", ep, "", "must be a name, not an ip"},
		"no endpoint":         {"e2e.example.com", nil, "", "need at least 1"},
		"private endpoint":    {"e2e.example.com", []string{"https://192.168.1.10"}, "", "is not a public address"},
		"http metadata":       {"e2e.example.com", ep, "http://example.com/m.json", "metadata uri must be https"},
		"private metadata":    {"e2e.example.com", ep, "https://10.0.0.1/m.json", "is not a public address"},
		"hyphen-edged label":  {"-bad.example.com", ep, "", "must not start or end with a hyphen"},
		"metadata over 256 B": {"e2e.example.com", ep, "https://example.com/" + strings.Repeat("a", 250), "metadata uri exceeds"},
	}
	for name, tc := range cases {
		r := c.Submit(t, k, chain.TxOptions{}, clusterMsg("MsgRegisterCluster", k.Address, id, tc.domain, tc.eps, tc.meta))
		chain.RequireRefused(t, name, r, tc.want)
	}
	stranger := nonOperator(t, c)
	r := c.Submit(t, stranger, chain.TxOptions{}, clusterMsg("MsgRegisterCluster", stranger.Address, id, "e2e.example.com", ep, ""))
	chain.RequireRefused(t, "cluster of a non-operator", r, "not found")
	if out := c.QueryOut(t, k.Node, "nodes", "cluster", id); out.Exit == 0 {
		t.Errorf("cluster %s exists after only refusals: %s", id, out.Stdout)
	}
}

func queryCluster(t *testing.T, c *chain.Chain, id string) clusterView {
	t.Helper()
	var v clusterView
	c.Query(t, c.Node(t, 0), &v, "nodes", "cluster", id)
	return v
}

func retireClusterAtCleanup(t *testing.T, c *chain.Chain, k chain.Key, id string) {
	out := c.QueryOut(t, k.Node, "nodes", "cluster", id)
	if out.Exit != 0 || strings.Contains(out.Stdout, "CLUSTER_STATUS_RETIRED") {
		return
	}
	c.CleanupSubmit(t, k, fmt.Sprintf("retire cluster %s", id), clusterMsg("MsgRetireCluster", k.Address, id, "", nil, ""))
}
