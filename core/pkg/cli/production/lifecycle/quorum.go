package lifecycle

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// checkQuorumSafety queries local RQLite to determine if stopping this node
// would break quorum. Returns a warning message if unsafe, empty string if safe.
func checkQuorumSafety() string {
	// Query local RQLite status to check if we're a voter
	status, err := getLocalRQLiteStatus()
	if err != nil {
		// RQLite may not be running — safe to stop
		return ""
	}

	raftState, _ := status["state"].(string)
	isVoter, _ := status["voter"].(bool)

	// If we're not a voter, stopping is always safe for quorum
	if !isVoter {
		return ""
	}

	// Query /nodes to count reachable voters
	nodes, err := getLocalRQLiteNodes()
	if err != nil {
		return fmt.Sprintf("Cannot verify quorum safety (failed to query nodes: %v). This node is a %s voter.", err, raftState)
	}

	reachableVoters := 0
	totalVoters := 0
	for _, node := range nodes {
		voter, _ := node["voter"].(bool)
		reachable, _ := node["reachable"].(bool)
		if voter {
			totalVoters++
			if reachable {
				reachableVoters++
			}
		}
	}

	// After removing this voter, remaining voters must form quorum:
	// quorum = (totalVoters / 2) + 1, so we need reachableVoters - 1 >= quorum
	remainingVoters := reachableVoters - 1
	quorumNeeded := (totalVoters-1)/2 + 1

	if remainingVoters < quorumNeeded {
		role := raftState
		if role == "Leader" {
			role = "the LEADER"
		}
		return fmt.Sprintf(
			"Stopping this node (%s, %s) would break RQLite quorum (%d/%d reachable voters would remain, need %d).",
			role, "voter", remainingVoters, totalVoters-1, quorumNeeded)
	}

	if raftState == "Leader" {
		// Not quorum-breaking but warn about leadership
		fmt.Printf("  Note: This node is the RQLite leader. Leadership will transfer on shutdown.\n")
	}

	return ""
}

// getLocalRQLiteStatus queries local RQLite /status and extracts raft info
func getLocalRQLiteStatus() (map[string]interface{}, error) {
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get("http://localhost:5001/status")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var status map[string]interface{}
	if err := json.Unmarshal(body, &status); err != nil {
		return nil, err
	}

	// Extract raft state from nested structure
	store, _ := status["store"].(map[string]interface{})
	if store == nil {
		return nil, fmt.Errorf("no store in status")
	}
	raft, _ := store["raft"].(map[string]interface{})
	if raft == nil {
		return nil, fmt.Errorf("no raft in status")
	}

	// Add voter status from the node info
	result := map[string]interface{}{
		"state": raft["state"],
		"voter": true, // Local node queries /status which doesn't include voter flag, assume voter if we got here
	}

	return result, nil
}

// getLocalRQLiteNodes queries local RQLite /nodes?nonvoters to get cluster members
func getLocalRQLiteNodes() ([]map[string]interface{}, error) {
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get("http://localhost:5001/nodes?nonvoters&timeout=3s")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	// RQLite /nodes returns a map of node_id -> node_info
	var nodesMap map[string]map[string]interface{}
	if err := json.Unmarshal(body, &nodesMap); err != nil {
		return nil, err
	}

	var nodes []map[string]interface{}
	for _, node := range nodesMap {
		nodes = append(nodes, node)
	}

	return nodes, nil
}

// containsService checks if a service name exists in the service list
func containsService(services []string, name string) bool {
	for _, s := range services {
		if s == name {
			return true
		}
	}
	return false
}
