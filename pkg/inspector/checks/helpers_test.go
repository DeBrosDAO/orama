package checks

import (
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/pkg/inspector"
)

// makeNode creates a test Node with the given host and role.
func makeNode(host, role string) inspector.Node {
	return inspector.Node{
		Environment: "devnet",
		User:        "ubuntu",
		Host:        host,
		Password:    "test",
		Role:        role,
	}
}

// makeNodeData creates a NodeData with a node but no subsystem data.
func makeNodeData(host, role string) *inspector.NodeData {
	return &inspector.NodeData{
		Node: makeNode(host, role),
	}
}

// makeCluster creates a ClusterData from a map of host → NodeData.
func makeCluster(nodes map[string]*inspector.NodeData) *inspector.ClusterData {
	return &inspector.ClusterData{
		Nodes:    nodes,
		Duration: 1 * time.Second,
	}
}

// countByStatus counts results with the given status.
func countByStatus(results []inspector.CheckResult, status inspector.Status) int {
	n := 0
	for _, r := range results {
		if r.Status == status {
			n++
		}
	}
	return n
}

// findCheck returns a pointer to the first check matching the given ID, or nil.
func findCheck(results []inspector.CheckResult, id string) *inspector.CheckResult {
	for i := range results {
		if results[i].ID == id {
			return &results[i]
		}
	}
	return nil
}

// requireCheck finds a check by ID and fails the test if not found.
func requireCheck(t *testing.T, results []inspector.CheckResult, id string) inspector.CheckResult {
	t.Helper()
	c := findCheck(results, id)
	if c == nil {
		t.Fatalf("check %q not found in %d results", id, len(results))
	}
	return *c
}

// expectStatus asserts that a check with the given ID has the expected status.
func expectStatus(t *testing.T, results []inspector.CheckResult, id string, status inspector.Status) {
	t.Helper()
	c := requireCheck(t, results, id)
	if c.Status != status {
		t.Errorf("check %q: want status=%s, got status=%s (msg=%s)", id, status, c.Status, c.Message)
	}
}
