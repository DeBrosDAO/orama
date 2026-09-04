package recover

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"sync"

	"github.com/DeBrosOfficial/network/pkg/inspector"
)

// nodeIndex is how far one node's raft log has been applied.
type nodeIndex struct {
	Node inspector.Node
	// Applied is the node's applied index, the amount of the log it has
	// actually put into its database. It is the number that decides which copy
	// of the data survives a recovery.
	Applied int64
	// State is the node's raft state, or "" when it could not be read.
	State string
	// Err is why the node could not be read, when it could not.
	Err error
}

// PickLeader chooses the node whose data a recovery should keep.
//
// The command required --leader and described it as "the node with the highest
// commit index", while never computing it. The only guidance anywhere was a
// hand-rolled curl in NODE_REPLACEMENT.md, and the value decides which copy of
// the cluster's data survives — every other node's is deleted. Asking an
// operator to work that out under the pressure of a lost quorum, by hand,
// against six nodes, is how the wrong one gets named.
//
// Ties go to the lexicographically first host, so the same cluster produces the
// same answer twice.
func PickLeader(nodes []inspector.Node) (inspector.Node, []nodeIndex, error) {
	indexes := readAppliedIndexes(nodes)

	var reachable []nodeIndex
	for _, ni := range indexes {
		if ni.Err == nil {
			reachable = append(reachable, ni)
		}
	}
	if len(reachable) == 0 {
		return inspector.Node{}, indexes, fmt.Errorf(
			"could not read the applied index of any node — name one with --leader, " +
				"or use --leader-raft-addr if rqlite is not answering anywhere")
	}

	sort.Slice(reachable, func(i, j int) bool {
		if reachable[i].Applied != reachable[j].Applied {
			return reachable[i].Applied > reachable[j].Applied
		}
		return reachable[i].Node.Host < reachable[j].Node.Host
	})
	return reachable[0].Node, indexes, nil
}

// readAppliedIndexes asks every node how far it has applied, in parallel.
//
// The nodes are read at the same moment on purpose: an index read one at a time
// across six SSH round trips compares numbers taken seconds apart, which on a
// cluster that is still committing is not a comparison at all.
func readAppliedIndexes(nodes []inspector.Node) []nodeIndex {
	out := make([]nodeIndex, len(nodes))

	var wg sync.WaitGroup
	for i, n := range nodes {
		wg.Add(1)
		go func(idx int, node inspector.Node) {
			defer wg.Done()
			out[idx] = readAppliedIndex(node)
		}(i, n)
	}
	wg.Wait()

	sort.Slice(out, func(i, j int) bool { return out[i].Node.Host < out[j].Node.Host })
	return out
}

// readAppliedIndex reads one node's applied index and raft state.
func readAppliedIndex(node inspector.Node) nodeIndex {
	cmd := fmt.Sprintf("curl -sS --max-time 5 http://localhost:%d/status", rqlitePort)
	res := inspector.RunSSH(context.Background(), node, cmd)
	if !res.OK() {
		return nodeIndex{Node: node, Err: fmt.Errorf("rqlite did not answer: %v", res.Err)}
	}

	applied, state, err := parseAppliedIndex([]byte(res.Stdout))
	if err != nil {
		return nodeIndex{Node: node, Err: err}
	}
	return nodeIndex{Node: node, Applied: applied, State: state}
}

// parseAppliedIndex reads applied_index and state out of an rqlite /status body.
func parseAppliedIndex(body []byte) (int64, string, error) {
	var status struct {
		Store struct {
			Raft struct {
				State string `json:"state"`
				// rqlite reports these as strings.
				AppliedIndex string `json:"applied_index"`
			} `json:"raft"`
		} `json:"store"`
	}
	if err := json.Unmarshal(body, &status); err != nil {
		return 0, "", fmt.Errorf("parse /status: %w", err)
	}

	raft := status.Store.Raft
	if raft.State == "" {
		return 0, "", fmt.Errorf("/status carried no raft state")
	}

	var applied int64
	if raft.AppliedIndex != "" {
		if _, err := fmt.Sscanf(raft.AppliedIndex, "%d", &applied); err != nil {
			return 0, raft.State, fmt.Errorf("applied_index %q is not a number: %w", raft.AppliedIndex, err)
		}
	}
	return applied, raft.State, nil
}

// FormatIndexes renders what each node reported, for the operator to check
// before approving a recovery that deletes every other copy.
func FormatIndexes(indexes []nodeIndex, chosen inspector.Node) string {
	var b []byte
	b = append(b, "Applied index on each node:\n"...)
	for _, ni := range indexes {
		marker := "  "
		if ni.Node.Host == chosen.Host {
			marker = "→ "
		}
		if ni.Err != nil {
			b = fmt.Appendf(b, "%s%-16s  unreachable: %v\n", marker, ni.Node.Host, ni.Err)
			continue
		}
		b = fmt.Appendf(b, "%s%-16s  applied %-12d %s\n", marker, ni.Node.Host, ni.Applied, ni.State)
	}
	b = append(b, "\nThe marked node's data is kept. Every other node's raft log and database\n"...)
	b = append(b, "are deleted and rebuilt from it.\n"...)
	return string(b)
}
