package cluster

import (
	"fmt"
	"os"
)

// checkResult represents the outcome of a single health check.
type checkResult struct {
	Name   string
	Status string // "PASS", "FAIL", "WARN"
	Detail string
}

// HandleHealth handles the "orama cluster health" command.
func HandleHealth(args []string) {
	fmt.Printf("Cluster Health Check\n")
	fmt.Printf("====================\n\n")

	var results []checkResult

	// Check 1: RQLite reachable
	status, err := queryRQLiteStatus()
	if err != nil {
		results = append(results, checkResult{
			Name:   "RQLite reachable",
			Status: "FAIL",
			Detail: fmt.Sprintf("Cannot connect to RQLite: %v", err),
		})
		printHealthResults(results)
		os.Exit(1)
		return
	}
	results = append(results, checkResult{
		Name:   "RQLite reachable",
		Status: "PASS",
		Detail: fmt.Sprintf("HTTP API responding on %s", status.HTTP.Address),
	})

	// Check 2: Raft state is leader or follower (not candidate or shutdown)
	raftState := status.Store.Raft.State
	switch raftState {
	case "Leader", "Follower":
		results = append(results, checkResult{
			Name:   "Raft state healthy",
			Status: "PASS",
			Detail: fmt.Sprintf("Node is %s", raftState),
		})
	case "Candidate":
		results = append(results, checkResult{
			Name:   "Raft state healthy",
			Status: "WARN",
			Detail: "Node is Candidate (election in progress)",
		})
	default:
		results = append(results, checkResult{
			Name:   "Raft state healthy",
			Status: "FAIL",
			Detail: fmt.Sprintf("Node is in unexpected state: %s", raftState),
		})
	}

	// Check 3: Leader exists
	if status.Store.Raft.Leader != "" {
		results = append(results, checkResult{
			Name:   "Leader exists",
			Status: "PASS",
			Detail: fmt.Sprintf("Leader: %s", status.Store.Raft.Leader),
		})
	} else {
		results = append(results, checkResult{
			Name:   "Leader exists",
			Status: "FAIL",
			Detail: "No leader detected in Raft cluster",
		})
	}

	// Check 4: Applied index is advancing (commit == applied means caught up)
	if status.Store.Raft.AppliedIndex >= status.Store.Raft.CommitIndex {
		results = append(results, checkResult{
			Name:   "Log replication",
			Status: "PASS",
			Detail: fmt.Sprintf("Applied index (%d) >= commit index (%d)",
				status.Store.Raft.AppliedIndex, status.Store.Raft.CommitIndex),
		})
	} else {
		lag := status.Store.Raft.CommitIndex - status.Store.Raft.AppliedIndex
		severity := "WARN"
		if lag > 1000 {
			severity = "FAIL"
		}
		results = append(results, checkResult{
			Name:   "Log replication",
			Status: severity,
			Detail: fmt.Sprintf("Applied index (%d) behind commit index (%d) by %d entries",
				status.Store.Raft.AppliedIndex, status.Store.Raft.CommitIndex, lag),
		})
	}

	// Check 5: Query nodes to validate cluster membership
	nodes, err := queryRQLiteNodes(true)
	if err != nil {
		results = append(results, checkResult{
			Name:   "Cluster nodes reachable",
			Status: "FAIL",
			Detail: fmt.Sprintf("Cannot query /nodes: %v", err),
		})
	} else {
		totalNodes := len(nodes)
		voters := 0
		nonVoters := 0
		reachable := 0
		leaders := 0

		for _, node := range nodes {
			if node.Voter {
				voters++
			} else {
				nonVoters++
			}
			if node.Reachable {
				reachable++
			}
			if node.Leader {
				leaders++
			}
		}

		// Check 5a: Node count
		results = append(results, checkResult{
			Name:   "Cluster membership",
			Status: "PASS",
			Detail: fmt.Sprintf("%d nodes (%d voters, %d non-voters)", totalNodes, voters, nonVoters),
		})

		// Check 5b: All nodes reachable
		if reachable == totalNodes {
			results = append(results, checkResult{
				Name:   "All nodes reachable",
				Status: "PASS",
				Detail: fmt.Sprintf("%d/%d nodes reachable", reachable, totalNodes),
			})
		} else {
			unreachable := totalNodes - reachable
			results = append(results, checkResult{
				Name:   "All nodes reachable",
				Status: "WARN",
				Detail: fmt.Sprintf("%d/%d nodes reachable (%d unreachable)", reachable, totalNodes, unreachable),
			})
		}

		// Check 5c: Exactly one leader
		if leaders == 1 {
			results = append(results, checkResult{
				Name:   "Single leader",
				Status: "PASS",
				Detail: "Exactly 1 leader in cluster",
			})
		} else if leaders == 0 {
			results = append(results, checkResult{
				Name:   "Single leader",
				Status: "FAIL",
				Detail: "No leader found among nodes",
			})
		} else {
			results = append(results, checkResult{
				Name:   "Single leader",
				Status: "FAIL",
				Detail: fmt.Sprintf("Multiple leaders detected: %d (split-brain?)", leaders),
			})
		}

		// Check 5d: Quorum check (majority of voters must be reachable)
		quorum := (voters / 2) + 1
		reachableVoters := 0
		for _, node := range nodes {
			if node.Voter && node.Reachable {
				reachableVoters++
			}
		}
		if reachableVoters >= quorum {
			results = append(results, checkResult{
				Name:   "Quorum healthy",
				Status: "PASS",
				Detail: fmt.Sprintf("%d/%d voters reachable (quorum requires %d)", reachableVoters, voters, quorum),
			})
		} else {
			results = append(results, checkResult{
				Name:   "Quorum healthy",
				Status: "FAIL",
				Detail: fmt.Sprintf("%d/%d voters reachable (quorum requires %d)", reachableVoters, voters, quorum),
			})
		}
	}

	printHealthResults(results)

	// Exit with non-zero if any failures
	for _, r := range results {
		if r.Status == "FAIL" {
			os.Exit(1)
		}
	}
}

// printHealthResults prints the health check results in a formatted table.
func printHealthResults(results []checkResult) {
	// Find the longest check name for alignment
	maxName := 0
	for _, r := range results {
		if len(r.Name) > maxName {
			maxName = len(r.Name)
		}
	}

	for _, r := range results {
		indicator := "  "
		switch r.Status {
		case "PASS":
			indicator = "PASS"
		case "FAIL":
			indicator = "FAIL"
		case "WARN":
			indicator = "WARN"
		}

		fmt.Printf("  [%s] %-*s  %s\n", indicator, maxName, r.Name, r.Detail)
	}
	fmt.Println()

	// Summary
	pass, fail, warn := 0, 0, 0
	for _, r := range results {
		switch r.Status {
		case "PASS":
			pass++
		case "FAIL":
			fail++
		case "WARN":
			warn++
		}
	}
	fmt.Printf("Summary: %d passed, %d failed, %d warnings\n", pass, fail, warn)
}
