package checks

import (
	"testing"

	"github.com/DeBrosOfficial/network/pkg/inspector"
)

func TestCheckNamespace_PerNodeHealthy(t *testing.T) {
	nd := makeNodeData("1.1.1.1", "node")
	nd.Namespaces = []inspector.NamespaceData{
		{
			Name:          "myapp",
			PortBase:      10000,
			RQLiteUp:      true,
			RQLiteState:   "Leader",
			RQLiteReady:   true,
			OlricUp:       true,
			GatewayUp:     true,
			GatewayStatus: 200,
		},
	}

	data := makeCluster(map[string]*inspector.NodeData{"1.1.1.1": nd})
	results := CheckNamespace(data)

	expectStatus(t, results, "ns.myapp.rqlite_up", inspector.StatusPass)
	expectStatus(t, results, "ns.myapp.rqlite_state", inspector.StatusPass)
	expectStatus(t, results, "ns.myapp.rqlite_ready", inspector.StatusPass)
	expectStatus(t, results, "ns.myapp.olric_up", inspector.StatusPass)
	expectStatus(t, results, "ns.myapp.gateway_up", inspector.StatusPass)
}

func TestCheckNamespace_RQLiteDown(t *testing.T) {
	nd := makeNodeData("1.1.1.1", "node")
	nd.Namespaces = []inspector.NamespaceData{
		{Name: "myapp", PortBase: 10000, RQLiteUp: false},
	}
	data := makeCluster(map[string]*inspector.NodeData{"1.1.1.1": nd})
	results := CheckNamespace(data)
	expectStatus(t, results, "ns.myapp.rqlite_up", inspector.StatusFail)
}

func TestCheckNamespace_RQLiteStates(t *testing.T) {
	tests := []struct {
		state  string
		status inspector.Status
	}{
		{"Leader", inspector.StatusPass},
		{"Follower", inspector.StatusPass},
		{"Candidate", inspector.StatusWarn},
		{"Unknown", inspector.StatusFail},
	}
	for _, tt := range tests {
		t.Run(tt.state, func(t *testing.T) {
			nd := makeNodeData("1.1.1.1", "node")
			nd.Namespaces = []inspector.NamespaceData{
				{Name: "myapp", PortBase: 10000, RQLiteUp: true, RQLiteState: tt.state},
			}
			data := makeCluster(map[string]*inspector.NodeData{"1.1.1.1": nd})
			results := CheckNamespace(data)
			expectStatus(t, results, "ns.myapp.rqlite_state", tt.status)
		})
	}
}

func TestCheckNamespace_RQLiteNotReady(t *testing.T) {
	nd := makeNodeData("1.1.1.1", "node")
	nd.Namespaces = []inspector.NamespaceData{
		{Name: "myapp", PortBase: 10000, RQLiteUp: true, RQLiteState: "Follower", RQLiteReady: false},
	}
	data := makeCluster(map[string]*inspector.NodeData{"1.1.1.1": nd})
	results := CheckNamespace(data)
	expectStatus(t, results, "ns.myapp.rqlite_ready", inspector.StatusFail)
}

func TestCheckNamespace_OlricDown(t *testing.T) {
	nd := makeNodeData("1.1.1.1", "node")
	nd.Namespaces = []inspector.NamespaceData{
		{Name: "myapp", OlricUp: false},
	}
	data := makeCluster(map[string]*inspector.NodeData{"1.1.1.1": nd})
	results := CheckNamespace(data)
	expectStatus(t, results, "ns.myapp.olric_up", inspector.StatusFail)
}

func TestCheckNamespace_GatewayDown(t *testing.T) {
	nd := makeNodeData("1.1.1.1", "node")
	nd.Namespaces = []inspector.NamespaceData{
		{Name: "myapp", GatewayUp: false, GatewayStatus: 0},
	}
	data := makeCluster(map[string]*inspector.NodeData{"1.1.1.1": nd})
	results := CheckNamespace(data)
	expectStatus(t, results, "ns.myapp.gateway_up", inspector.StatusFail)
}

func TestCheckNamespace_CrossNode_AllHealthy(t *testing.T) {
	nodes := map[string]*inspector.NodeData{}
	for _, host := range []string{"1.1.1.1", "2.2.2.2", "3.3.3.3"} {
		nd := makeNodeData(host, "node")
		nd.Namespaces = []inspector.NamespaceData{
			{Name: "myapp", RQLiteUp: true, OlricUp: true, GatewayUp: true},
		}
		nodes[host] = nd
	}
	data := makeCluster(nodes)
	results := CheckNamespace(data)
	expectStatus(t, results, "ns.myapp.all_healthy", inspector.StatusPass)
	expectStatus(t, results, "ns.myapp.quorum", inspector.StatusPass)
}

func TestCheckNamespace_CrossNode_PartialHealthy(t *testing.T) {
	nodes := map[string]*inspector.NodeData{}
	for i, host := range []string{"1.1.1.1", "2.2.2.2", "3.3.3.3"} {
		nd := makeNodeData(host, "node")
		nd.Namespaces = []inspector.NamespaceData{
			{Name: "myapp", RQLiteUp: true, OlricUp: i < 2, GatewayUp: true},
		}
		nodes[host] = nd
	}
	data := makeCluster(nodes)
	results := CheckNamespace(data)
	expectStatus(t, results, "ns.myapp.all_healthy", inspector.StatusFail)
	// Quorum should still pass (3/3 RQLite up, need 2)
	expectStatus(t, results, "ns.myapp.quorum", inspector.StatusPass)
}

func TestCheckNamespace_CrossNode_QuorumLost(t *testing.T) {
	nodes := map[string]*inspector.NodeData{}
	rqliteUp := []bool{true, false, false}
	for i, host := range []string{"1.1.1.1", "2.2.2.2", "3.3.3.3"} {
		nd := makeNodeData(host, "node")
		nd.Namespaces = []inspector.NamespaceData{
			{Name: "myapp", RQLiteUp: rqliteUp[i], OlricUp: true, GatewayUp: true},
		}
		nodes[host] = nd
	}
	data := makeCluster(nodes)
	results := CheckNamespace(data)
	expectStatus(t, results, "ns.myapp.quorum", inspector.StatusFail)
}

func TestCheckNamespace_MultipleNamespaces(t *testing.T) {
	nd := makeNodeData("1.1.1.1", "node")
	nd.Namespaces = []inspector.NamespaceData{
		{Name: "app1", RQLiteUp: true, RQLiteState: "Leader", OlricUp: true, GatewayUp: true},
		{Name: "app2", RQLiteUp: false, OlricUp: true, GatewayUp: true},
	}
	data := makeCluster(map[string]*inspector.NodeData{"1.1.1.1": nd})
	results := CheckNamespace(data)

	expectStatus(t, results, "ns.app1.rqlite_up", inspector.StatusPass)
	expectStatus(t, results, "ns.app2.rqlite_up", inspector.StatusFail)
}

func TestCheckNamespace_NoNamespaces(t *testing.T) {
	nd := makeNodeData("1.1.1.1", "node")
	nd.Namespaces = nil
	data := makeCluster(map[string]*inspector.NodeData{"1.1.1.1": nd})
	results := CheckNamespace(data)
	// No per-node results, only cross-node (which should be empty since no namespaces)
	for _, r := range results {
		t.Errorf("unexpected check: %s", r.ID)
	}
}

// A namespace the registry says is being created or deleted is up on some nodes
// and not yet (or no longer) on others: an e2e namespace torn down during an
// inspection failed it critically as "0/1 nodes fully healthy".
func TestCheckNamespace_InTransitionIsNotJudged(t *testing.T) {
	for _, status := range []string{"provisioning", "deprovisioning"} {
		t.Run(status, func(t *testing.T) {
			nd := makeNodeData("1.1.1.1", "node")
			nd.Namespaces = []inspector.NamespaceData{{Name: "e2e-x", RegistryStatus: status}}
			results := CheckNamespace(makeCluster(map[string]*inspector.NodeData{"1.1.1.1": nd}))
			expectStatus(t, results, "ns.e2e-x.settled", inspector.StatusSkip)
			expectStatus(t, results, "ns.e2e-x.all_healthy", inspector.StatusSkip)
			for _, r := range results {
				if r.Status == inspector.StatusFail {
					t.Errorf("a namespace in transition failed %s: %s", r.ID, r.Message)
				}
			}
		})
	}
}

// Every other registry state, and an unreadable registry, is judged: a ready,
// degraded or failed namespace with services down is unhealthy, and so is one
// the registry does not know at all.
func TestCheckNamespace_SettledOrUnknownIsJudged(t *testing.T) {
	for _, status := range []string{"ready", "degraded", "failed", ""} {
		t.Run("status="+status, func(t *testing.T) {
			nd := makeNodeData("1.1.1.1", "node")
			nd.Namespaces = []inspector.NamespaceData{{Name: "myapp", RegistryStatus: status}}
			results := CheckNamespace(makeCluster(map[string]*inspector.NodeData{"1.1.1.1": nd}))
			expectStatus(t, results, "ns.myapp.all_healthy", inspector.StatusFail)
		})
	}
}
