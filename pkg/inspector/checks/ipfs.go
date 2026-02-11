package checks

import (
	"fmt"

	"github.com/DeBrosOfficial/network/pkg/inspector"
)

func init() {
	inspector.RegisterChecker("ipfs", CheckIPFS)
}

const ipfsSub = "ipfs"

// CheckIPFS runs all IPFS health checks against cluster data.
func CheckIPFS(data *inspector.ClusterData) []inspector.CheckResult {
	var results []inspector.CheckResult

	for _, nd := range data.Nodes {
		if nd.IPFS == nil {
			continue
		}
		results = append(results, checkIPFSPerNode(nd, data)...)
	}

	results = append(results, checkIPFSCrossNode(data)...)

	return results
}

func checkIPFSPerNode(nd *inspector.NodeData, data *inspector.ClusterData) []inspector.CheckResult {
	var r []inspector.CheckResult
	ipfs := nd.IPFS
	node := nd.Node.Name()

	// 3.1 IPFS daemon running
	if ipfs.DaemonActive {
		r = append(r, inspector.Pass("ipfs.daemon_active", "IPFS daemon active", ipfsSub, node,
			"debros-ipfs is active", inspector.Critical))
	} else {
		r = append(r, inspector.Fail("ipfs.daemon_active", "IPFS daemon active", ipfsSub, node,
			"debros-ipfs is not active", inspector.Critical))
		return r
	}

	// 3.2 IPFS Cluster running
	if ipfs.ClusterActive {
		r = append(r, inspector.Pass("ipfs.cluster_active", "IPFS Cluster active", ipfsSub, node,
			"debros-ipfs-cluster is active", inspector.Critical))
	} else {
		r = append(r, inspector.Fail("ipfs.cluster_active", "IPFS Cluster active", ipfsSub, node,
			"debros-ipfs-cluster is not active", inspector.Critical))
	}

	// 3.6 Swarm peer count
	expectedNodes := countIPFSNodes(data)
	if ipfs.SwarmPeerCount >= 0 {
		expectedPeers := expectedNodes - 1
		if expectedPeers < 0 {
			expectedPeers = 0
		}
		if ipfs.SwarmPeerCount >= expectedPeers {
			r = append(r, inspector.Pass("ipfs.swarm_peers", "Swarm peer count sufficient", ipfsSub, node,
				fmt.Sprintf("peers=%d (expected >=%d)", ipfs.SwarmPeerCount, expectedPeers), inspector.High))
		} else if ipfs.SwarmPeerCount > 0 {
			r = append(r, inspector.Warn("ipfs.swarm_peers", "Swarm peer count sufficient", ipfsSub, node,
				fmt.Sprintf("peers=%d (expected >=%d)", ipfs.SwarmPeerCount, expectedPeers), inspector.High))
		} else {
			r = append(r, inspector.Fail("ipfs.swarm_peers", "Swarm peer count sufficient", ipfsSub, node,
				fmt.Sprintf("peers=%d (isolated!)", ipfs.SwarmPeerCount), inspector.Critical))
		}
	}

	// 3.12 Cluster peer count
	if ipfs.ClusterPeerCount >= 0 {
		if ipfs.ClusterPeerCount >= expectedNodes {
			r = append(r, inspector.Pass("ipfs.cluster_peers", "Cluster peer count matches expected", ipfsSub, node,
				fmt.Sprintf("cluster_peers=%d (expected=%d)", ipfs.ClusterPeerCount, expectedNodes), inspector.Critical))
		} else {
			r = append(r, inspector.Warn("ipfs.cluster_peers", "Cluster peer count matches expected", ipfsSub, node,
				fmt.Sprintf("cluster_peers=%d (expected=%d)", ipfs.ClusterPeerCount, expectedNodes), inspector.Critical))
		}
	}

	// 3.14 Cluster peer errors
	if ipfs.ClusterErrors == 0 {
		r = append(r, inspector.Pass("ipfs.cluster_errors", "No cluster peer errors", ipfsSub, node,
			"all cluster peers healthy", inspector.Critical))
	} else {
		r = append(r, inspector.Fail("ipfs.cluster_errors", "No cluster peer errors", ipfsSub, node,
			fmt.Sprintf("%d peers reporting errors", ipfs.ClusterErrors), inspector.Critical))
	}

	// 3.20 Repo size vs max
	if ipfs.RepoMaxBytes > 0 && ipfs.RepoSizeBytes > 0 {
		pct := float64(ipfs.RepoSizeBytes) / float64(ipfs.RepoMaxBytes) * 100
		sizeMB := ipfs.RepoSizeBytes / (1024 * 1024)
		maxMB := ipfs.RepoMaxBytes / (1024 * 1024)
		if pct < 80 {
			r = append(r, inspector.Pass("ipfs.repo_size", "Repo size below limit", ipfsSub, node,
				fmt.Sprintf("repo=%dMB/%dMB (%.0f%%)", sizeMB, maxMB, pct), inspector.High))
		} else if pct < 95 {
			r = append(r, inspector.Warn("ipfs.repo_size", "Repo size below limit", ipfsSub, node,
				fmt.Sprintf("repo=%dMB/%dMB (%.0f%%)", sizeMB, maxMB, pct), inspector.High))
		} else {
			r = append(r, inspector.Fail("ipfs.repo_size", "Repo size below limit", ipfsSub, node,
				fmt.Sprintf("repo=%dMB/%dMB (%.0f%% NEARLY FULL)", sizeMB, maxMB, pct), inspector.Critical))
		}
	}

	// 3.3 Version
	if ipfs.KuboVersion != "" && ipfs.KuboVersion != "unknown" {
		r = append(r, inspector.Pass("ipfs.kubo_version", "Kubo version reported", ipfsSub, node,
			fmt.Sprintf("kubo=%s", ipfs.KuboVersion), inspector.Low))
	}
	if ipfs.ClusterVersion != "" && ipfs.ClusterVersion != "unknown" {
		r = append(r, inspector.Pass("ipfs.cluster_version", "Cluster version reported", ipfsSub, node,
			fmt.Sprintf("cluster=%s", ipfs.ClusterVersion), inspector.Low))
	}

	// 3.29 Swarm key exists (private swarm)
	if ipfs.HasSwarmKey {
		r = append(r, inspector.Pass("ipfs.swarm_key", "Swarm key exists (private swarm)", ipfsSub, node,
			"swarm.key present", inspector.Critical))
	} else {
		r = append(r, inspector.Fail("ipfs.swarm_key", "Swarm key exists (private swarm)", ipfsSub, node,
			"swarm.key NOT found", inspector.Critical))
	}

	// 3.30 Bootstrap empty (private swarm)
	if ipfs.BootstrapEmpty {
		r = append(r, inspector.Pass("ipfs.bootstrap_empty", "Bootstrap list empty (private)", ipfsSub, node,
			"no public bootstrap peers", inspector.High))
	} else {
		r = append(r, inspector.Warn("ipfs.bootstrap_empty", "Bootstrap list empty (private)", ipfsSub, node,
			"bootstrap list is not empty (should be empty for private swarm)", inspector.High))
	}

	return r
}

func checkIPFSCrossNode(data *inspector.ClusterData) []inspector.CheckResult {
	var r []inspector.CheckResult

	type nodeInfo struct {
		name string
		ipfs *inspector.IPFSData
	}
	var nodes []nodeInfo
	for _, nd := range data.Nodes {
		if nd.IPFS != nil && nd.IPFS.DaemonActive {
			nodes = append(nodes, nodeInfo{name: nd.Node.Name(), ipfs: nd.IPFS})
		}
	}

	if len(nodes) < 2 {
		return r
	}

	// Version consistency
	kuboVersions := map[string][]string{}
	clusterVersions := map[string][]string{}
	for _, n := range nodes {
		if n.ipfs.KuboVersion != "" && n.ipfs.KuboVersion != "unknown" {
			kuboVersions[n.ipfs.KuboVersion] = append(kuboVersions[n.ipfs.KuboVersion], n.name)
		}
		if n.ipfs.ClusterVersion != "" && n.ipfs.ClusterVersion != "unknown" {
			clusterVersions[n.ipfs.ClusterVersion] = append(clusterVersions[n.ipfs.ClusterVersion], n.name)
		}
	}

	if len(kuboVersions) == 1 {
		for v := range kuboVersions {
			r = append(r, inspector.Pass("ipfs.kubo_version_consistent", "Kubo version consistent", ipfsSub, "",
				fmt.Sprintf("version=%s across %d nodes", v, len(nodes)), inspector.Medium))
		}
	} else if len(kuboVersions) > 1 {
		r = append(r, inspector.Warn("ipfs.kubo_version_consistent", "Kubo version consistent", ipfsSub, "",
			fmt.Sprintf("%d different versions", len(kuboVersions)), inspector.Medium))
	}

	if len(clusterVersions) == 1 {
		for v := range clusterVersions {
			r = append(r, inspector.Pass("ipfs.cluster_version_consistent", "Cluster version consistent", ipfsSub, "",
				fmt.Sprintf("version=%s across %d nodes", v, len(nodes)), inspector.Medium))
		}
	} else if len(clusterVersions) > 1 {
		r = append(r, inspector.Warn("ipfs.cluster_version_consistent", "Cluster version consistent", ipfsSub, "",
			fmt.Sprintf("%d different versions", len(clusterVersions)), inspector.Medium))
	}

	// Repo size convergence
	var sizes []int64
	for _, n := range nodes {
		if n.ipfs.RepoSizeBytes > 0 {
			sizes = append(sizes, n.ipfs.RepoSizeBytes)
		}
	}
	if len(sizes) >= 2 {
		minSize, maxSize := sizes[0], sizes[0]
		for _, s := range sizes[1:] {
			if s < minSize {
				minSize = s
			}
			if s > maxSize {
				maxSize = s
			}
		}
		if minSize > 0 {
			ratio := float64(maxSize) / float64(minSize)
			if ratio <= 2.0 {
				r = append(r, inspector.Pass("ipfs.repo_convergence", "Repo size similar across nodes", ipfsSub, "",
					fmt.Sprintf("ratio=%.1fx", ratio), inspector.Medium))
			} else {
				r = append(r, inspector.Warn("ipfs.repo_convergence", "Repo size similar across nodes", ipfsSub, "",
					fmt.Sprintf("ratio=%.1fx (diverged)", ratio), inspector.Medium))
			}
		}
	}

	return r
}

func countIPFSNodes(data *inspector.ClusterData) int {
	count := 0
	for _, nd := range data.Nodes {
		if nd.IPFS != nil {
			count++
		}
	}
	return count
}
