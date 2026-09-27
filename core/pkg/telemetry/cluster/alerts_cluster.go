package cluster

import (
	"fmt"

	"github.com/DeBrosOfficial/network/pkg/telemetry/report"
)

func checkRQLiteLeader(reports []*report.NodeReport) []Alert {
	var alerts []Alert
	leaders := 0
	leaderAddrs := map[string]bool{}
	for _, r := range reports {
		if r.RQLite != nil && r.RQLite.RaftState == report.RaftLeader {
			leaders++
		}
		if r.RQLite != nil && r.RQLite.LeaderAddr != "" {
			leaderAddrs[r.RQLite.LeaderAddr] = true
		}
	}

	if leaders == 0 {
		alerts = append(alerts, Alert{AlertCritical, "rqlite", "cluster", "No RQLite leader found"})
	} else if leaders > 1 {
		alerts = append(alerts, Alert{AlertCritical, "rqlite", "cluster",
			fmt.Sprintf("Split brain: %d leaders detected", leaders)})
	}

	if len(leaderAddrs) > 1 {
		alerts = append(alerts, Alert{AlertWarning, "rqlite", "cluster",
			fmt.Sprintf("Leader disagreement: nodes report %d different leader addresses", len(leaderAddrs))})
	}

	return alerts
}

func checkRQLiteQuorum(reports []*report.NodeReport) []Alert {
	var voters, responsive int
	for _, r := range reports {
		if r.RQLite == nil {
			continue
		}
		if r.RQLite.Responsive {
			responsive++
			if r.RQLite.Voter {
				voters++
			}
		}
	}

	if responsive == 0 {
		return nil // no rqlite data at all
	}

	// Total voters = responsive voters + unresponsive nodes that should be voters.
	// For quorum calculation, use the total voter count (responsive + unreachable).
	totalVoters := voters
	for _, r := range reports {
		if r.RQLite != nil && !r.RQLite.Responsive {
			// Assume unresponsive nodes were voters (conservative estimate).
			totalVoters++
		}
	}

	if totalVoters < 2 {
		return nil // single-node cluster, no quorum concept
	}

	quorum := totalVoters/2 + 1
	if voters < quorum {
		return []Alert{{AlertCritical, "rqlite", "cluster",
			fmt.Sprintf("Quorum lost: only %d/%d voters reachable (need %d)", voters, totalVoters, quorum)}}
	}
	if voters == quorum {
		return []Alert{{AlertWarning, "rqlite", "cluster",
			fmt.Sprintf("Quorum fragile: exactly %d/%d voters reachable (one more failure = quorum loss)", voters, totalVoters)}}
	}

	return nil
}

func checkRaftTermConsistency(reports []*report.NodeReport) []Alert {
	var minTerm, maxTerm uint64
	first := true
	for _, r := range reports {
		if r.RQLite == nil || !r.RQLite.Responsive {
			continue
		}
		if first {
			minTerm = r.RQLite.Term
			maxTerm = r.RQLite.Term
			first = false
		}
		if r.RQLite.Term < minTerm {
			minTerm = r.RQLite.Term
		}
		if r.RQLite.Term > maxTerm {
			maxTerm = r.RQLite.Term
		}
	}
	if maxTerm-minTerm > 1 {
		return []Alert{{AlertWarning, "rqlite", "cluster",
			fmt.Sprintf("Raft term inconsistency: min=%d, max=%d (delta=%d)", minTerm, maxTerm, maxTerm-minTerm)}}
	}
	return nil
}

func checkAppliedIndexLag(reports []*report.NodeReport) []Alert {
	var maxApplied uint64
	for _, r := range reports {
		if r.RQLite != nil && r.RQLite.Applied > maxApplied {
			maxApplied = r.RQLite.Applied
		}
	}

	var alerts []Alert
	for _, r := range reports {
		if r.RQLite == nil || !r.RQLite.Responsive {
			continue
		}
		lag := maxApplied - r.RQLite.Applied
		if lag > 100 {
			alerts = append(alerts, Alert{AlertWarning, "rqlite", nodeHost(r),
				fmt.Sprintf("Applied index lag: %d behind leader (local=%d, max=%d)", lag, r.RQLite.Applied, maxApplied)})
		}
	}
	return alerts
}

func checkWGPeerSymmetry(reports []*report.NodeReport) []Alert {
	type nodeInfo struct {
		host     string
		peerKeys map[string]bool
	}
	var nodes []nodeInfo
	for _, r := range reports {
		if r.WireGuard == nil || !r.WireGuard.InterfaceUp {
			continue
		}
		ni := nodeInfo{host: nodeHost(r), peerKeys: map[string]bool{}}
		for _, p := range r.WireGuard.Peers {
			ni.peerKeys[p.PublicKey] = true
		}
		nodes = append(nodes, ni)
	}

	var alerts []Alert
	expectedPeers := len(nodes) - 1
	for _, ni := range nodes {
		if len(ni.peerKeys) < expectedPeers {
			alerts = append(alerts, Alert{AlertCritical, "wireguard", ni.host,
				fmt.Sprintf("WG peer count mismatch: has %d peers, expected %d", len(ni.peerKeys), expectedPeers)})
		}
	}

	return alerts
}

func checkClockSkew(reports []*report.NodeReport) []Alert {
	var times []struct {
		host string
		t    int64
	}
	for _, r := range reports {
		if r.System != nil && r.System.TimeUnix > 0 {
			times = append(times, struct {
				host string
				t    int64
			}{nodeHost(r), r.System.TimeUnix})
		}
	}
	if len(times) < 2 {
		return nil
	}

	var minT, maxT int64 = times[0].t, times[0].t
	var minHost, maxHost string = times[0].host, times[0].host
	for _, t := range times[1:] {
		if t.t < minT {
			minT = t.t
			minHost = t.host
		}
		if t.t > maxT {
			maxT = t.t
			maxHost = t.host
		}
	}

	delta := maxT - minT
	if delta > 5 {
		return []Alert{{AlertWarning, "system", "cluster",
			fmt.Sprintf("Clock skew: %ds between %s and %s", delta, minHost, maxHost)}}
	}
	return nil
}

func checkBinaryVersion(reports []*report.NodeReport) []Alert {
	versions := map[string][]string{} // version -> list of hosts
	for _, r := range reports {
		v := r.Version
		if v == "" {
			v = "unknown"
		}
		versions[v] = append(versions[v], nodeHost(r))
	}
	if len(versions) > 1 {
		msg := "Binary version mismatch:"
		for v, hosts := range versions {
			msg += fmt.Sprintf(" %s=%v", v, hosts)
		}
		return []Alert{{AlertWarning, "system", "cluster", msg}}
	}
	return nil
}

func checkOlricMemberConsistency(reports []*report.NodeReport) []Alert {
	// Count nodes where Olric is active to determine expected member count.
	activeCount := 0
	for _, r := range reports {
		if r.Olric != nil && r.Olric.ServiceActive {
			activeCount++
		}
	}
	if activeCount < 2 {
		return nil
	}

	var alerts []Alert
	for _, r := range reports {
		if r.Olric == nil || !r.Olric.ServiceActive || r.Olric.MemberCount == 0 {
			continue
		}
		if r.Olric.MemberCount < activeCount {
			alerts = append(alerts, Alert{AlertWarning, "olric", nodeHost(r),
				fmt.Sprintf("Olric member count: %d (expected %d active nodes)", r.Olric.MemberCount, activeCount)})
		}
	}
	return alerts
}

func checkIPFSSwarmConsistency(reports []*report.NodeReport) []Alert {
	// Count IPFS-active nodes to determine expected peer count.
	activeCount := 0
	for _, r := range reports {
		if r.IPFS != nil && r.IPFS.DaemonActive {
			activeCount++
		}
	}
	if activeCount < 2 {
		return nil
	}

	expectedPeers := activeCount - 1
	var alerts []Alert
	for _, r := range reports {
		if r.IPFS == nil || !r.IPFS.DaemonActive {
			continue
		}
		if r.IPFS.SwarmPeerCount == 0 {
			alerts = append(alerts, Alert{AlertCritical, "ipfs", nodeHost(r),
				"IPFS node isolated: 0 swarm peers"})
		} else if r.IPFS.SwarmPeerCount < expectedPeers {
			alerts = append(alerts, Alert{AlertWarning, "ipfs", nodeHost(r),
				fmt.Sprintf("IPFS swarm peers: %d (expected %d)", r.IPFS.SwarmPeerCount, expectedPeers)})
		}
	}
	return alerts
}

func checkIPFSClusterConsistency(reports []*report.NodeReport) []Alert {
	activeCount := 0
	for _, r := range reports {
		if r.IPFS != nil && r.IPFS.ClusterActive {
			activeCount++
		}
	}
	if activeCount < 2 {
		return nil
	}

	var alerts []Alert
	for _, r := range reports {
		if r.IPFS == nil || !r.IPFS.ClusterActive {
			continue
		}
		if r.IPFS.ClusterPeerCount < activeCount {
			alerts = append(alerts, Alert{AlertWarning, "ipfs", nodeHost(r),
				fmt.Sprintf("IPFS cluster peers: %d (expected %d)", r.IPFS.ClusterPeerCount, activeCount)})
		}
	}
	return alerts
}

// ---------------------------------------------------------------------------
// Per-node checks
// ---------------------------------------------------------------------------
