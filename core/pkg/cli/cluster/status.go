package cluster

import (
	"encoding/json"
	"fmt"
	"github.com/DeBrosOfficial/network/pkg/constants"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

const httpTimeout = 10 * time.Second

// rqliteBaseURL is the index RQLite HTTP API on this node.
var rqliteBaseURL = constants.LocalRQLiteURL()

// rqliteStatus represents the relevant fields from the RQLite /status endpoint.
type rqliteStatus struct {
	Store struct {
		Raft struct {
			State        string `json:"state"`
			AppliedIndex uint64 `json:"applied_index"`
			CommitIndex  uint64 `json:"commit_index"`
			Term         uint64 `json:"current_term"`
			Leader       string `json:"leader"`
		} `json:"raft"`
		Dir     string `json:"dir"`
		NodeID  string `json:"node_id"`
		Address string `json:"addr"`
	} `json:"store"`
	HTTP struct {
		Address string `json:"addr"`
	} `json:"http"`
	Node struct {
		Uptime string `json:"uptime"`
	} `json:"node"`
}

// rqliteNode represents a node from the /nodes endpoint.
type rqliteNode struct {
	ID        string  `json:"id"`
	Address   string  `json:"addr"`
	Leader    bool    `json:"leader"`
	Voter     bool    `json:"voter"`
	Reachable bool    `json:"reachable"`
	Time      float64 `json:"time"`
	TimeS     string  `json:"time_s"`
}

// HandleStatus handles the "orama cluster status" command.
func HandleStatus(args []string) {
	if hasFlag(args, "--all") {
		fmt.Printf("Remote node aggregation via SSH is not yet implemented.\n")
		fmt.Printf("Currently showing local node status only.\n\n")
	}

	fmt.Printf("Cluster Status\n")
	fmt.Printf("==============\n\n")

	// Query RQLite status
	status, err := queryRQLiteStatus()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error querying RQLite status: %v\n", err)
		fmt.Printf("RQLite may not be running on this node.\n\n")
	} else {
		printLocalStatus(status)
	}

	// Query RQLite nodes
	nodes, err := queryRQLiteNodes(true)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error querying RQLite nodes: %v\n", err)
	} else {
		printNodesTable(nodes)
	}

	// Query Olric status (best-effort)
	printOlricStatus()
}

// queryRQLiteStatus queries the local RQLite /status endpoint.
func queryRQLiteStatus() (*rqliteStatus, error) {
	client := &http.Client{Timeout: httpTimeout}
	resp, err := client.Get(rqliteBaseURL + "/status")
	if err != nil {
		return nil, fmt.Errorf("connect to RQLite: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	var status rqliteStatus
	if err := json.Unmarshal(body, &status); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}

	return &status, nil
}

// queryRQLiteNodes queries the local RQLite /nodes endpoint.
// If includeNonVoters is true, appends ?nonvoters to the query.
func queryRQLiteNodes(includeNonVoters bool) (map[string]*rqliteNode, error) {
	client := &http.Client{Timeout: httpTimeout}

	url := rqliteBaseURL + "/nodes"
	if includeNonVoters {
		url += "?nonvoters"
	}

	resp, err := client.Get(url)
	if err != nil {
		return nil, fmt.Errorf("connect to RQLite: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	var nodes map[string]*rqliteNode
	if err := json.Unmarshal(body, &nodes); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}

	return nodes, nil
}

// printLocalStatus prints the local node's RQLite status.
func printLocalStatus(s *rqliteStatus) {
	fmt.Printf("Local Node\n")
	fmt.Printf("  Node ID:       %s\n", s.Store.NodeID)
	fmt.Printf("  Raft Address:  %s\n", s.Store.Address)
	fmt.Printf("  HTTP Address:  %s\n", s.HTTP.Address)
	fmt.Printf("  Raft State:    %s\n", strings.ToUpper(s.Store.Raft.State))
	fmt.Printf("  Raft Term:     %d\n", s.Store.Raft.Term)
	fmt.Printf("  Applied Index: %d\n", s.Store.Raft.AppliedIndex)
	fmt.Printf("  Commit Index:  %d\n", s.Store.Raft.CommitIndex)
	fmt.Printf("  Leader:        %s\n", s.Store.Raft.Leader)
	if s.Node.Uptime != "" {
		fmt.Printf("  Uptime:        %s\n", s.Node.Uptime)
	}
	fmt.Println()
}

// printNodesTable prints a formatted table of all cluster nodes.
func printNodesTable(nodes map[string]*rqliteNode) {
	if len(nodes) == 0 {
		fmt.Printf("No nodes found in cluster.\n\n")
		return
	}

	fmt.Printf("Cluster Nodes (%d total)\n", len(nodes))
	fmt.Printf("%-20s %-30s %-8s %-10s %-10s %-12s\n",
		"NODE ID", "ADDRESS", "VOTER", "LEADER", "REACHABLE", "LATENCY")
	fmt.Printf("%-20s %-30s %-8s %-10s %-10s %-12s\n",
		strings.Repeat("-", 20),
		strings.Repeat("-", 30),
		strings.Repeat("-", 8),
		strings.Repeat("-", 10),
		strings.Repeat("-", 10),
		strings.Repeat("-", 12))

	for id, node := range nodes {
		nodeID := id
		if len(nodeID) > 20 {
			nodeID = nodeID[:17] + "..."
		}

		voter := "no"
		if node.Voter {
			voter = "yes"
		}

		leader := "no"
		if node.Leader {
			leader = "yes"
		}

		reachable := "no"
		if node.Reachable {
			reachable = "yes"
		}

		latency := "-"
		if node.TimeS != "" {
			latency = node.TimeS
		} else if node.Time > 0 {
			latency = fmt.Sprintf("%.3fs", node.Time)
		}

		fmt.Printf("%-20s %-30s %-8s %-10s %-10s %-12s\n",
			nodeID, node.Address, voter, leader, reachable, latency)
	}
	fmt.Println()
}

// printOlricStatus attempts to query the local Olric status endpoint.
func printOlricStatus() {
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(constants.LocalOlricURL() + "/")
	if err != nil {
		fmt.Printf("Olric: not reachable on %s (%v)\n\n", constants.LocalOlricURL(), err)
		return
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		fmt.Printf("Olric: reachable but could not read response\n\n")
		return
	}

	if resp.StatusCode == http.StatusOK {
		fmt.Printf("Olric: reachable (HTTP %d)\n", resp.StatusCode)
		// Try to parse as JSON for a nicer display
		var data map[string]interface{}
		if err := json.Unmarshal(body, &data); err == nil {
			for key, val := range data {
				fmt.Printf("  %s: %v\n", key, val)
			}
		} else {
			// Not JSON, print raw (truncated)
			raw := strings.TrimSpace(string(body))
			if len(raw) > 200 {
				raw = raw[:200] + "..."
			}
			if raw != "" {
				fmt.Printf("  Response: %s\n", raw)
			}
		}
	} else {
		fmt.Printf("Olric: reachable but returned HTTP %d\n", resp.StatusCode)
	}
	fmt.Println()
}
