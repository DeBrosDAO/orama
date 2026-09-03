package checks

import (
	"fmt"
	"math"
	"strings"

	"github.com/DeBrosOfficial/network/pkg/inspector"
)

func init() {
	inspector.RegisterChecker("rqlite", CheckRQLite)
}

const rqliteSub = "rqlite"

// CheckRQLite runs all RQLite health checks against cluster data.
func CheckRQLite(data *inspector.ClusterData) []inspector.CheckResult {
	var results []inspector.CheckResult

	// Find the leader's authoritative /nodes data
	leaderNodes := findLeaderNodes(data)

	// Per-node checks
	for _, nd := range data.Nodes {
		if nd.RQLite == nil {
			continue
		}
		results = append(results, checkRQLitePerNode(nd, data, leaderNodes)...)
	}

	// Cross-node checks
	results = append(results, checkRQLiteCrossNode(data, leaderNodes)...)

	return results
}

// findLeaderNodes returns the leader's /nodes map as the authoritative cluster membership.
func findLeaderNodes(data *inspector.ClusterData) map[string]*inspector.RQLiteNode {
	for _, nd := range data.Nodes {
		if nd.RQLite != nil && nd.RQLite.Status != nil && nd.RQLite.Status.RaftState == "Leader" && nd.RQLite.Nodes != nil {
			return nd.RQLite.Nodes
		}
	}
	return nil
}

// nodeIP extracts the IP from a "host:port" address.
func nodeIP(addr string) string {
	if idx := strings.LastIndex(addr, ":"); idx >= 0 {
		return addr[:idx]
	}
	return addr
}

// lookupInLeaderNodes finds a node in the leader's /nodes map by matching IP.
// Leader's /nodes keys use HTTP port (5001), while node IDs use Raft port (7001).
func lookupInLeaderNodes(leaderNodes map[string]*inspector.RQLiteNode, nodeID string) *inspector.RQLiteNode {
	if leaderNodes == nil {
		return nil
	}
	ip := nodeIP(nodeID)
	for addr, n := range leaderNodes {
		if nodeIP(addr) == ip {
			return n
		}
	}
	return nil
}

func checkRQLitePerNode(nd *inspector.NodeData, data *inspector.ClusterData, leaderNodes map[string]*inspector.RQLiteNode) []inspector.CheckResult {
	var r []inspector.CheckResult
	rq := nd.RQLite
	node := nd.Node.Name()

	// 1.2 HTTP endpoint responsive
	if !rq.Responsive {
		r = append(r, inspector.Fail("rqlite.responsive", "RQLite HTTP endpoint responsive", rqliteSub, node,
			"curl localhost:10100/status failed or returned error", inspector.Critical))
		return r
	}
	r = append(r, inspector.Pass("rqlite.responsive", "RQLite HTTP endpoint responsive", rqliteSub, node,
		"responding on port 5001", inspector.Critical))

	// 1.3 Full readiness (/readyz)
	if rq.Readyz != nil {
		if rq.Readyz.Ready {
			r = append(r, inspector.Pass("rqlite.readyz", "Full readiness check", rqliteSub, node,
				"node, leader, store all ready", inspector.Critical))
		} else {
			var parts []string
			if rq.Readyz.Node != "ready" {
				parts = append(parts, "node: "+rq.Readyz.Node)
			}
			if rq.Readyz.Leader != "ready" {
				parts = append(parts, "leader: "+rq.Readyz.Leader)
			}
			if rq.Readyz.Store != "ready" {
				parts = append(parts, "store: "+rq.Readyz.Store)
			}
			r = append(r, inspector.Fail("rqlite.readyz", "Full readiness check", rqliteSub, node,
				"not ready: "+strings.Join(parts, ", "), inspector.Critical))
		}
	}

	s := rq.Status
	if s == nil {
		r = append(r, inspector.Skip("rqlite.status_parsed", "Status JSON parseable", rqliteSub, node,
			"could not parse /status response", inspector.Critical))
		return r
	}

	// 1.5 Raft state valid
	switch s.RaftState {
	case "Leader", "Follower":
		r = append(r, inspector.Pass("rqlite.raft_state", "Raft state valid", rqliteSub, node,
			fmt.Sprintf("state=%s", s.RaftState), inspector.Critical))
	case "Candidate":
		r = append(r, inspector.Warn("rqlite.raft_state", "Raft state valid", rqliteSub, node,
			"state=Candidate (election in progress)", inspector.Critical))
	case "Shutdown":
		r = append(r, inspector.Fail("rqlite.raft_state", "Raft state valid", rqliteSub, node,
			"state=Shutdown", inspector.Critical))
	default:
		r = append(r, inspector.Fail("rqlite.raft_state", "Raft state valid", rqliteSub, node,
			fmt.Sprintf("unexpected state=%q", s.RaftState), inspector.Critical))
	}

	// 1.7 Leader identity known
	if s.LeaderNodeID == "" {
		r = append(r, inspector.Fail("rqlite.leader_known", "Leader identity known", rqliteSub, node,
			"leader node_id is empty", inspector.Critical))
	} else {
		r = append(r, inspector.Pass("rqlite.leader_known", "Leader identity known", rqliteSub, node,
			fmt.Sprintf("leader=%s", s.LeaderNodeID), inspector.Critical))
	}

	// 1.8 Voter status — use leader's /nodes as authoritative source
	if leaderNode := lookupInLeaderNodes(leaderNodes, s.NodeID); leaderNode != nil {
		if leaderNode.Voter {
			r = append(r, inspector.Pass("rqlite.voter", "Node is voter", rqliteSub, node,
				"voter=true (confirmed by leader)", inspector.Low))
		} else {
			r = append(r, inspector.Pass("rqlite.voter", "Node is non-voter", rqliteSub, node,
				"non-voter (by design, confirmed by leader)", inspector.Low))
		}
	} else if s.Voter {
		r = append(r, inspector.Pass("rqlite.voter", "Node is voter", rqliteSub, node,
			"voter=true", inspector.Low))
	} else {
		r = append(r, inspector.Pass("rqlite.voter", "Node is non-voter", rqliteSub, node,
			"non-voter (no leader data to confirm)", inspector.Low))
	}

	// 1.9 Num peers — use leader's /nodes as authoritative cluster size
	if leaderNodes != nil && len(leaderNodes) > 0 {
		expectedPeers := len(leaderNodes) - 1 // cluster members minus self
		if expectedPeers < 0 {
			expectedPeers = 0
		}
		if s.NumPeers == expectedPeers {
			r = append(r, inspector.Pass("rqlite.num_peers", "Peer count matches cluster size", rqliteSub, node,
				fmt.Sprintf("peers=%d (cluster has %d nodes)", s.NumPeers, len(leaderNodes)), inspector.Critical))
		} else {
			r = append(r, inspector.Warn("rqlite.num_peers", "Peer count matches cluster size", rqliteSub, node,
				fmt.Sprintf("peers=%d but leader reports %d members", s.NumPeers, len(leaderNodes)), inspector.High))
		}
	} else if rq.Nodes != nil && len(rq.Nodes) > 0 {
		// Fallback: use node's own /nodes if leader data unavailable
		expectedPeers := len(rq.Nodes) - 1
		if expectedPeers < 0 {
			expectedPeers = 0
		}
		if s.NumPeers == expectedPeers {
			r = append(r, inspector.Pass("rqlite.num_peers", "Peer count matches cluster size", rqliteSub, node,
				fmt.Sprintf("peers=%d (cluster has %d nodes)", s.NumPeers, len(rq.Nodes)), inspector.Critical))
		} else {
			r = append(r, inspector.Warn("rqlite.num_peers", "Peer count matches cluster size", rqliteSub, node,
				fmt.Sprintf("peers=%d but /nodes reports %d members", s.NumPeers, len(rq.Nodes)), inspector.High))
		}
	} else {
		r = append(r, inspector.Pass("rqlite.num_peers", "Peer count reported", rqliteSub, node,
			fmt.Sprintf("peers=%d", s.NumPeers), inspector.Medium))
	}

	// 1.11 Commit index vs applied index
	if s.CommitIndex > 0 && s.AppliedIndex > 0 {
		gap := s.CommitIndex - s.AppliedIndex
		if s.AppliedIndex > s.CommitIndex {
			gap = 0
		}
		if gap <= 2 {
			r = append(r, inspector.Pass("rqlite.commit_applied_gap", "Commit/applied index close", rqliteSub, node,
				fmt.Sprintf("commit=%d applied=%d gap=%d", s.CommitIndex, s.AppliedIndex, gap), inspector.Critical))
		} else if gap <= 100 {
			r = append(r, inspector.Warn("rqlite.commit_applied_gap", "Commit/applied index close", rqliteSub, node,
				fmt.Sprintf("commit=%d applied=%d gap=%d (lagging)", s.CommitIndex, s.AppliedIndex, gap), inspector.Critical))
		} else {
			r = append(r, inspector.Fail("rqlite.commit_applied_gap", "Commit/applied index close", rqliteSub, node,
				fmt.Sprintf("commit=%d applied=%d gap=%d (severely behind)", s.CommitIndex, s.AppliedIndex, gap), inspector.Critical))
		}
	}

	// 1.12 FSM pending
	if s.FsmPending == 0 {
		r = append(r, inspector.Pass("rqlite.fsm_pending", "FSM pending queue empty", rqliteSub, node,
			"fsm_pending=0", inspector.High))
	} else if s.FsmPending <= 10 {
		r = append(r, inspector.Warn("rqlite.fsm_pending", "FSM pending queue empty", rqliteSub, node,
			fmt.Sprintf("fsm_pending=%d", s.FsmPending), inspector.High))
	} else {
		r = append(r, inspector.Fail("rqlite.fsm_pending", "FSM pending queue empty", rqliteSub, node,
			fmt.Sprintf("fsm_pending=%d (backlog)", s.FsmPending), inspector.High))
	}

	// 1.13 Last contact (followers only)
	if s.RaftState == "Follower" && s.LastContact != "" {
		r = append(r, inspector.Pass("rqlite.last_contact", "Follower last contact recent", rqliteSub, node,
			fmt.Sprintf("last_contact=%s", s.LastContact), inspector.Critical))
	}

	// 1.14 Last log term matches current term
	if s.LastLogTerm > 0 && s.Term > 0 {
		if s.LastLogTerm == s.Term {
			r = append(r, inspector.Pass("rqlite.log_term_match", "Last log term matches current", rqliteSub, node,
				fmt.Sprintf("term=%d last_log_term=%d", s.Term, s.LastLogTerm), inspector.Medium))
		} else {
			r = append(r, inspector.Warn("rqlite.log_term_match", "Last log term matches current", rqliteSub, node,
				fmt.Sprintf("term=%d last_log_term=%d (mismatch)", s.Term, s.LastLogTerm), inspector.Medium))
		}
	}

	// 1.15 db_applied_index close to fsm_index
	if s.DBAppliedIndex > 0 && s.FsmIndex > 0 {
		var dbFsmGap uint64
		if s.FsmIndex > s.DBAppliedIndex {
			dbFsmGap = s.FsmIndex - s.DBAppliedIndex
		} else {
			dbFsmGap = s.DBAppliedIndex - s.FsmIndex
		}
		if dbFsmGap <= 5 {
			r = append(r, inspector.Pass("rqlite.db_fsm_sync", "DB applied index matches FSM index", rqliteSub, node,
				fmt.Sprintf("db_applied=%d fsm=%d gap=%d", s.DBAppliedIndex, s.FsmIndex, dbFsmGap), inspector.Critical))
		} else {
			r = append(r, inspector.Fail("rqlite.db_fsm_sync", "DB applied index matches FSM index", rqliteSub, node,
				fmt.Sprintf("db_applied=%d fsm=%d gap=%d (diverged)", s.DBAppliedIndex, s.FsmIndex, dbFsmGap), inspector.Critical))
		}
	}

	// 1.18 Last snapshot index close to applied
	if s.LastSnapshot > 0 && s.AppliedIndex > 0 {
		gap := s.AppliedIndex - s.LastSnapshot
		if s.LastSnapshot > s.AppliedIndex {
			gap = 0
		}
		if gap < 10000 {
			r = append(r, inspector.Pass("rqlite.snapshot_recent", "Snapshot recent", rqliteSub, node,
				fmt.Sprintf("snapshot_index=%d applied=%d gap=%d", s.LastSnapshot, s.AppliedIndex, gap), inspector.Medium))
		} else {
			r = append(r, inspector.Warn("rqlite.snapshot_recent", "Snapshot recent", rqliteSub, node,
				fmt.Sprintf("snapshot_index=%d applied=%d gap=%d (old snapshot)", s.LastSnapshot, s.AppliedIndex, gap), inspector.Medium))
		}
	}

	// 1.19 At least 1 snapshot exists
	if s.LastSnapshot > 0 {
		r = append(r, inspector.Pass("rqlite.has_snapshot", "At least one snapshot exists", rqliteSub, node,
			fmt.Sprintf("last_snapshot_index=%d", s.LastSnapshot), inspector.Medium))
	} else {
		r = append(r, inspector.Warn("rqlite.has_snapshot", "At least one snapshot exists", rqliteSub, node,
			"no snapshots found", inspector.Medium))
	}

	// 1.27 Database size
	if s.DBSizeFriendly != "" {
		r = append(r, inspector.Pass("rqlite.db_size", "Database size reported", rqliteSub, node,
			fmt.Sprintf("db_size=%s", s.DBSizeFriendly), inspector.Low))
	}

	// 1.31 Goroutine count
	if s.Goroutines > 0 {
		if s.Goroutines < 200 {
			r = append(r, inspector.Pass("rqlite.goroutines", "Goroutine count healthy", rqliteSub, node,
				fmt.Sprintf("goroutines=%d", s.Goroutines), inspector.Medium))
		} else if s.Goroutines < 1000 {
			r = append(r, inspector.Warn("rqlite.goroutines", "Goroutine count healthy", rqliteSub, node,
				fmt.Sprintf("goroutines=%d (elevated)", s.Goroutines), inspector.Medium))
		} else {
			r = append(r, inspector.Fail("rqlite.goroutines", "Goroutine count healthy", rqliteSub, node,
				fmt.Sprintf("goroutines=%d (high)", s.Goroutines), inspector.High))
		}
	}

	// 1.32 Memory (HeapAlloc)
	if s.HeapAlloc > 0 {
		mb := s.HeapAlloc / (1024 * 1024)
		if mb < 500 {
			r = append(r, inspector.Pass("rqlite.memory", "Memory usage healthy", rqliteSub, node,
				fmt.Sprintf("heap=%dMB", mb), inspector.Medium))
		} else if mb < 1000 {
			r = append(r, inspector.Warn("rqlite.memory", "Memory usage healthy", rqliteSub, node,
				fmt.Sprintf("heap=%dMB (elevated)", mb), inspector.Medium))
		} else {
			r = append(r, inspector.Fail("rqlite.memory", "Memory usage healthy", rqliteSub, node,
				fmt.Sprintf("heap=%dMB (high)", mb), inspector.High))
		}
	}

	// 1.35 Version reported
	if s.Version != "" {
		r = append(r, inspector.Pass("rqlite.version", "Version reported", rqliteSub, node,
			fmt.Sprintf("version=%s", s.Version), inspector.Low))
	}

	// Node reachability from /nodes endpoint
	if rq.Nodes != nil {
		unreachable := 0
		for addr, n := range rq.Nodes {
			if !n.Reachable {
				unreachable++
				r = append(r, inspector.Fail("rqlite.node_reachable", "Cluster node reachable", rqliteSub, node,
					fmt.Sprintf("%s is unreachable from this node", addr), inspector.Critical))
			}
		}
		if unreachable == 0 {
			r = append(r, inspector.Pass("rqlite.all_reachable", "All cluster nodes reachable", rqliteSub, node,
				fmt.Sprintf("all %d nodes reachable", len(rq.Nodes)), inspector.Critical))
		}
	}

	// 1.46 Strong read test
	if rq.StrongRead {
		r = append(r, inspector.Pass("rqlite.strong_read", "Strong read succeeds", rqliteSub, node,
			"SELECT 1 at level=strong OK", inspector.Critical))
	} else if rq.Responsive {
		r = append(r, inspector.Fail("rqlite.strong_read", "Strong read succeeds", rqliteSub, node,
			"SELECT 1 at level=strong failed", inspector.Critical))
	}

	// Debug vars checks
	if dv := rq.DebugVars; dv != nil {
		// 1.28 Query errors
		if dv.QueryErrors == 0 {
			r = append(r, inspector.Pass("rqlite.query_errors", "No query errors", rqliteSub, node,
				"query_errors=0", inspector.High))
		} else {
			r = append(r, inspector.Warn("rqlite.query_errors", "No query errors", rqliteSub, node,
				fmt.Sprintf("query_errors=%d", dv.QueryErrors), inspector.High))
		}

		// 1.29 Execute errors
		if dv.ExecuteErrors == 0 {
			r = append(r, inspector.Pass("rqlite.execute_errors", "No execute errors", rqliteSub, node,
				"execute_errors=0", inspector.High))
		} else {
			r = append(r, inspector.Warn("rqlite.execute_errors", "No execute errors", rqliteSub, node,
				fmt.Sprintf("execute_errors=%d", dv.ExecuteErrors), inspector.High))
		}

		// 1.30 Leader not found events
		if dv.LeaderNotFound == 0 {
			r = append(r, inspector.Pass("rqlite.leader_not_found", "No leader-not-found events", rqliteSub, node,
				"leader_not_found=0", inspector.Critical))
		} else {
			r = append(r, inspector.Fail("rqlite.leader_not_found", "No leader-not-found events", rqliteSub, node,
				fmt.Sprintf("leader_not_found=%d", dv.LeaderNotFound), inspector.Critical))
		}

		// Snapshot errors
		if dv.SnapshotErrors == 0 {
			r = append(r, inspector.Pass("rqlite.snapshot_errors", "No snapshot errors", rqliteSub, node,
				"snapshot_errors=0", inspector.High))
		} else {
			r = append(r, inspector.Fail("rqlite.snapshot_errors", "No snapshot errors", rqliteSub, node,
				fmt.Sprintf("snapshot_errors=%d", dv.SnapshotErrors), inspector.High))
		}

		// Client retries/timeouts
		if dv.ClientRetries == 0 && dv.ClientTimeouts == 0 {
			r = append(r, inspector.Pass("rqlite.client_health", "No client retries or timeouts", rqliteSub, node,
				"retries=0 timeouts=0", inspector.Medium))
		} else {
			r = append(r, inspector.Warn("rqlite.client_health", "No client retries or timeouts", rqliteSub, node,
				fmt.Sprintf("retries=%d timeouts=%d", dv.ClientRetries, dv.ClientTimeouts), inspector.Medium))
		}
	}

	return r
}

func checkRQLiteCrossNode(data *inspector.ClusterData, leaderNodes map[string]*inspector.RQLiteNode) []inspector.CheckResult {
	var r []inspector.CheckResult

	type nodeInfo struct {
		host   string
		name   string
		status *inspector.RQLiteStatus
	}
	var nodes []nodeInfo
	for host, nd := range data.Nodes {
		if nd.RQLite != nil && nd.RQLite.Status != nil {
			nodes = append(nodes, nodeInfo{host: host, name: nd.Node.Name(), status: nd.RQLite.Status})
		}
	}

	if len(nodes) < 2 {
		r = append(r, inspector.Skip("rqlite.cross_node", "Cross-node checks", rqliteSub, "",
			fmt.Sprintf("only %d node(s) with RQLite data, need >=2", len(nodes)), inspector.Critical))
		return r
	}

	// 1.5 Exactly one leader
	leaders := 0
	var leaderName string
	for _, n := range nodes {
		if n.status.RaftState == "Leader" {
			leaders++
			leaderName = n.name
		}
	}
	switch leaders {
	case 1:
		r = append(r, inspector.Pass("rqlite.single_leader", "Exactly one leader in cluster", rqliteSub, "",
			fmt.Sprintf("leader=%s", leaderName), inspector.Critical))
	case 0:
		r = append(r, inspector.Fail("rqlite.single_leader", "Exactly one leader in cluster", rqliteSub, "",
			"no leader found", inspector.Critical))
	default:
		r = append(r, inspector.Fail("rqlite.single_leader", "Exactly one leader in cluster", rqliteSub, "",
			fmt.Sprintf("found %d leaders (split brain!)", leaders), inspector.Critical))
	}

	// 1.6 Term consistency
	terms := map[uint64][]string{}
	for _, n := range nodes {
		terms[n.status.Term] = append(terms[n.status.Term], n.name)
	}
	if len(terms) == 1 {
		for t := range terms {
			r = append(r, inspector.Pass("rqlite.term_consistent", "All nodes same Raft term", rqliteSub, "",
				fmt.Sprintf("term=%d across %d nodes", t, len(nodes)), inspector.Critical))
		}
	} else {
		var parts []string
		for t, names := range terms {
			parts = append(parts, fmt.Sprintf("term=%d: %s", t, strings.Join(names, ",")))
		}
		r = append(r, inspector.Fail("rqlite.term_consistent", "All nodes same Raft term", rqliteSub, "",
			"term divergence: "+strings.Join(parts, "; "), inspector.Critical))
	}

	// 1.36 All nodes agree on same leader
	leaderIDs := map[string][]string{}
	for _, n := range nodes {
		leaderIDs[n.status.LeaderNodeID] = append(leaderIDs[n.status.LeaderNodeID], n.name)
	}
	if len(leaderIDs) == 1 {
		for lid := range leaderIDs {
			r = append(r, inspector.Pass("rqlite.leader_agreement", "All nodes agree on leader", rqliteSub, "",
				fmt.Sprintf("leader_id=%s", lid), inspector.Critical))
		}
	} else {
		var parts []string
		for lid, names := range leaderIDs {
			id := lid
			if id == "" {
				id = "(none)"
			}
			parts = append(parts, fmt.Sprintf("%s: %s", id, strings.Join(names, ",")))
		}
		r = append(r, inspector.Fail("rqlite.leader_agreement", "All nodes agree on leader", rqliteSub, "",
			"leader disagreement: "+strings.Join(parts, "; "), inspector.Critical))
	}

	// 1.38 Applied index convergence
	var minApplied, maxApplied uint64
	hasApplied := false
	for _, n := range nodes {
		idx := n.status.AppliedIndex
		if idx == 0 {
			continue
		}
		if !hasApplied {
			minApplied = idx
			maxApplied = idx
			hasApplied = true
			continue
		}
		if idx < minApplied {
			minApplied = idx
		}
		if idx > maxApplied {
			maxApplied = idx
		}
	}
	if hasApplied && maxApplied > 0 {
		gap := maxApplied - minApplied
		if gap < 100 {
			r = append(r, inspector.Pass("rqlite.index_convergence", "Applied index convergence", rqliteSub, "",
				fmt.Sprintf("min=%d max=%d gap=%d", minApplied, maxApplied, gap), inspector.Critical))
		} else if gap < 1000 {
			r = append(r, inspector.Warn("rqlite.index_convergence", "Applied index convergence", rqliteSub, "",
				fmt.Sprintf("min=%d max=%d gap=%d (lagging)", minApplied, maxApplied, gap), inspector.Critical))
		} else {
			r = append(r, inspector.Fail("rqlite.index_convergence", "Applied index convergence", rqliteSub, "",
				fmt.Sprintf("min=%d max=%d gap=%d (severely behind)", minApplied, maxApplied, gap), inspector.Critical))
		}
	}

	// 1.35 Version consistency
	versions := map[string][]string{}
	for _, n := range nodes {
		if n.status.Version != "" {
			versions[n.status.Version] = append(versions[n.status.Version], n.name)
		}
	}
	if len(versions) == 1 {
		for v := range versions {
			r = append(r, inspector.Pass("rqlite.version_consistent", "Version consistent across nodes", rqliteSub, "",
				fmt.Sprintf("version=%s", v), inspector.Medium))
		}
	} else if len(versions) > 1 {
		var parts []string
		for v, names := range versions {
			parts = append(parts, fmt.Sprintf("%s: %s", v, strings.Join(names, ",")))
		}
		r = append(r, inspector.Warn("rqlite.version_consistent", "Version consistent across nodes", rqliteSub, "",
			"version mismatch: "+strings.Join(parts, "; "), inspector.Medium))
	}

	// 1.40 Database size convergence
	type sizeEntry struct {
		name string
		size int64
	}
	var sizes []sizeEntry
	for _, n := range nodes {
		if n.status.DBSize > 0 {
			sizes = append(sizes, sizeEntry{n.name, n.status.DBSize})
		}
	}
	if len(sizes) >= 2 {
		minSize := sizes[0].size
		maxSize := sizes[0].size
		for _, s := range sizes[1:] {
			if s.size < minSize {
				minSize = s.size
			}
			if s.size > maxSize {
				maxSize = s.size
			}
		}
		if minSize > 0 {
			ratio := float64(maxSize) / float64(minSize)
			if ratio <= 1.05 {
				r = append(r, inspector.Pass("rqlite.db_size_convergence", "Database size converged", rqliteSub, "",
					fmt.Sprintf("min=%dB max=%dB ratio=%.2f", minSize, maxSize, ratio), inspector.Medium))
			} else {
				r = append(r, inspector.Warn("rqlite.db_size_convergence", "Database size converged", rqliteSub, "",
					fmt.Sprintf("min=%dB max=%dB ratio=%.2f (diverged)", minSize, maxSize, ratio), inspector.High))
			}
		}
	}

	// 1.42 Quorum math — use leader's /nodes as authoritative voter source
	voters := 0
	reachableVoters := 0
	if leaderNodes != nil && len(leaderNodes) > 0 {
		for _, ln := range leaderNodes {
			if ln.Voter {
				voters++
				if ln.Reachable {
					reachableVoters++
				}
			}
		}
	} else {
		// Fallback: use each node's self-reported voter status
		for _, n := range nodes {
			if n.status.Voter {
				voters++
				reachableVoters++ // responded to SSH + curl = reachable
			}
		}
	}
	quorumNeeded := int(math.Floor(float64(voters)/2)) + 1
	if reachableVoters >= quorumNeeded {
		r = append(r, inspector.Pass("rqlite.quorum", "Quorum maintained", rqliteSub, "",
			fmt.Sprintf("reachable_voters=%d/%d quorum_needed=%d", reachableVoters, voters, quorumNeeded), inspector.Critical))
	} else {
		r = append(r, inspector.Fail("rqlite.quorum", "Quorum maintained", rqliteSub, "",
			fmt.Sprintf("reachable_voters=%d/%d quorum_needed=%d (QUORUM LOST)", reachableVoters, voters, quorumNeeded), inspector.Critical))
	}

	return r
}

// countRQLiteNodes counts nodes that have RQLite data.
func countRQLiteNodes(data *inspector.ClusterData) int {
	count := 0
	for _, nd := range data.Nodes {
		if nd.RQLite != nil {
			count++
		}
	}
	return count
}
