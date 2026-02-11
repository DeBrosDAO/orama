package checks

import (
	"testing"

	"github.com/DeBrosOfficial/network/pkg/inspector"
)

func TestCheckIPFS_DaemonInactive(t *testing.T) {
	nd := makeNodeData("1.1.1.1", "node")
	nd.IPFS = &inspector.IPFSData{DaemonActive: false}

	data := makeCluster(map[string]*inspector.NodeData{"1.1.1.1": nd})
	results := CheckIPFS(data)

	expectStatus(t, results, "ipfs.daemon_active", inspector.StatusFail)
	// Early return — no swarm peer checks
	if findCheck(results, "ipfs.swarm_peers") != nil {
		t.Error("should not check swarm_peers when daemon inactive")
	}
}

func TestCheckIPFS_HealthyNode(t *testing.T) {
	nd := makeNodeData("1.1.1.1", "node")
	nd.IPFS = &inspector.IPFSData{
		DaemonActive:     true,
		ClusterActive:    true,
		SwarmPeerCount:   0,  // single node: expected peers = 0
		ClusterPeerCount: 1,  // single node cluster
		ClusterErrors:    0,
		RepoSizeBytes:    500 * 1024 * 1024,  // 500MB
		RepoMaxBytes:     1024 * 1024 * 1024,  // 1GB
		KuboVersion:      "0.22.0",
		ClusterVersion:   "1.0.8",
		HasSwarmKey:      true,
		BootstrapEmpty:   true,
	}

	data := makeCluster(map[string]*inspector.NodeData{"1.1.1.1": nd})
	results := CheckIPFS(data)

	expectStatus(t, results, "ipfs.daemon_active", inspector.StatusPass)
	expectStatus(t, results, "ipfs.cluster_active", inspector.StatusPass)
	expectStatus(t, results, "ipfs.swarm_peers", inspector.StatusPass)
	expectStatus(t, results, "ipfs.cluster_peers", inspector.StatusPass)
	expectStatus(t, results, "ipfs.cluster_errors", inspector.StatusPass)
	expectStatus(t, results, "ipfs.repo_size", inspector.StatusPass)
	expectStatus(t, results, "ipfs.swarm_key", inspector.StatusPass)
	expectStatus(t, results, "ipfs.bootstrap_empty", inspector.StatusPass)
}

func TestCheckIPFS_SwarmPeers(t *testing.T) {
	// Single-node cluster: expected peers = 0
	t.Run("enough", func(t *testing.T) {
		nd := makeNodeData("1.1.1.1", "node")
		nd.IPFS = &inspector.IPFSData{DaemonActive: true, SwarmPeerCount: 2}
		data := makeCluster(map[string]*inspector.NodeData{"1.1.1.1": nd})
		results := CheckIPFS(data)
		// swarm_peers=2, expected=0 → pass
		expectStatus(t, results, "ipfs.swarm_peers", inspector.StatusPass)
	})

	t.Run("low but nonzero", func(t *testing.T) {
		// 3-node cluster: expected peers = 2 per node
		nd := makeNodeData("1.1.1.1", "node")
		nd.IPFS = &inspector.IPFSData{DaemonActive: true, SwarmPeerCount: 1} // has 1, expects 2
		nd2 := makeNodeData("2.2.2.2", "node")
		nd2.IPFS = &inspector.IPFSData{DaemonActive: true, SwarmPeerCount: 2}
		nd3 := makeNodeData("3.3.3.3", "node")
		nd3.IPFS = &inspector.IPFSData{DaemonActive: true, SwarmPeerCount: 2}
		data := makeCluster(map[string]*inspector.NodeData{
			"1.1.1.1": nd, "2.2.2.2": nd2, "3.3.3.3": nd3,
		})
		results := CheckIPFS(data)
		// Node 1.1.1.1 should warn (1 < 2)
		found := false
		for _, r := range results {
			if r.ID == "ipfs.swarm_peers" && r.Node == "ubuntu@1.1.1.1" && r.Status == inspector.StatusWarn {
				found = true
			}
		}
		if !found {
			t.Error("expected swarm_peers warn for node 1.1.1.1")
		}
	})

	t.Run("zero isolated", func(t *testing.T) {
		nd := makeNodeData("1.1.1.1", "node")
		nd.IPFS = &inspector.IPFSData{DaemonActive: true, SwarmPeerCount: 0}
		nd2 := makeNodeData("2.2.2.2", "node")
		nd2.IPFS = &inspector.IPFSData{DaemonActive: true, SwarmPeerCount: 1}
		data := makeCluster(map[string]*inspector.NodeData{
			"1.1.1.1": nd, "2.2.2.2": nd2,
		})
		results := CheckIPFS(data)
		found := false
		for _, r := range results {
			if r.ID == "ipfs.swarm_peers" && r.Node == "ubuntu@1.1.1.1" && r.Status == inspector.StatusFail {
				found = true
			}
		}
		if !found {
			t.Error("expected swarm_peers fail for isolated node 1.1.1.1")
		}
	})
}

func TestCheckIPFS_RepoSize(t *testing.T) {
	tests := []struct {
		name   string
		size   int64
		max    int64
		status inspector.Status
	}{
		{"healthy", 500 * 1024 * 1024, 1024 * 1024 * 1024, inspector.StatusPass},    // 50%
		{"elevated", 870 * 1024 * 1024, 1024 * 1024 * 1024, inspector.StatusWarn},   // 85%
		{"nearly full", 980 * 1024 * 1024, 1024 * 1024 * 1024, inspector.StatusFail}, // 96%
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			nd := makeNodeData("1.1.1.1", "node")
			nd.IPFS = &inspector.IPFSData{
				DaemonActive:  true,
				RepoSizeBytes: tt.size,
				RepoMaxBytes:  tt.max,
			}
			data := makeCluster(map[string]*inspector.NodeData{"1.1.1.1": nd})
			results := CheckIPFS(data)
			expectStatus(t, results, "ipfs.repo_size", tt.status)
		})
	}
}

func TestCheckIPFS_SwarmKeyMissing(t *testing.T) {
	nd := makeNodeData("1.1.1.1", "node")
	nd.IPFS = &inspector.IPFSData{DaemonActive: true, HasSwarmKey: false}
	data := makeCluster(map[string]*inspector.NodeData{"1.1.1.1": nd})
	results := CheckIPFS(data)
	expectStatus(t, results, "ipfs.swarm_key", inspector.StatusFail)
}

func TestCheckIPFS_BootstrapNotEmpty(t *testing.T) {
	nd := makeNodeData("1.1.1.1", "node")
	nd.IPFS = &inspector.IPFSData{DaemonActive: true, BootstrapEmpty: false}
	data := makeCluster(map[string]*inspector.NodeData{"1.1.1.1": nd})
	results := CheckIPFS(data)
	expectStatus(t, results, "ipfs.bootstrap_empty", inspector.StatusWarn)
}

func TestCheckIPFS_CrossNode_VersionConsistency(t *testing.T) {
	nodes := map[string]*inspector.NodeData{}
	for _, host := range []string{"1.1.1.1", "2.2.2.2", "3.3.3.3"} {
		nd := makeNodeData(host, "node")
		nd.IPFS = &inspector.IPFSData{DaemonActive: true, KuboVersion: "0.22.0", ClusterVersion: "1.0.8"}
		nodes[host] = nd
	}
	data := makeCluster(nodes)
	results := CheckIPFS(data)
	expectStatus(t, results, "ipfs.kubo_version_consistent", inspector.StatusPass)
	expectStatus(t, results, "ipfs.cluster_version_consistent", inspector.StatusPass)
}

func TestCheckIPFS_CrossNode_VersionMismatch(t *testing.T) {
	nodes := map[string]*inspector.NodeData{}
	versions := []string{"0.22.0", "0.22.0", "0.21.0"}
	for i, host := range []string{"1.1.1.1", "2.2.2.2", "3.3.3.3"} {
		nd := makeNodeData(host, "node")
		nd.IPFS = &inspector.IPFSData{DaemonActive: true, KuboVersion: versions[i]}
		nodes[host] = nd
	}
	data := makeCluster(nodes)
	results := CheckIPFS(data)
	expectStatus(t, results, "ipfs.kubo_version_consistent", inspector.StatusWarn)
}

func TestCheckIPFS_NilData(t *testing.T) {
	nd := makeNodeData("1.1.1.1", "node")
	data := makeCluster(map[string]*inspector.NodeData{"1.1.1.1": nd})
	results := CheckIPFS(data)
	if len(results) != 0 {
		t.Errorf("expected 0 results for nil IPFS data, got %d", len(results))
	}
}
