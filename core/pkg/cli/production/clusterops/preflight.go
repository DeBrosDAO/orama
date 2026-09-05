package clusterops

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/DeBrosOfficial/network/pkg/inspector"
	"github.com/DeBrosOfficial/network/pkg/rqlite"
)

// Impact is what removing one node costs one raft cluster.
//
// A node carries the platform cluster and a voter in every namespace cluster
// it was allocated to, and those are separate raft groups with separate
// quorums. Removing a node used to be checked against the platform cluster
// alone, so an operator could retire a node that held two of three voters for a
// namespace and only find out when that namespace stopped accepting writes.
// NODE_REPLACEMENT.md records exactly that outcome.
type Impact struct {
	// Cluster is PlatformCluster or a namespace name.
	Cluster string
	// VotersBefore is how many voters the cluster has now.
	VotersBefore int
	// VotersAfter is how many it would have without this node.
	VotersAfter int
	// QuorumAfter is how many of those must be reachable to commit a write.
	QuorumAfter int
	// ReachableAfter is how many of them are answering.
	ReachableAfter int
	// Refusal is why this removal must not happen, empty when it may.
	Refusal string
}

// PlatformCluster is the Impact.Cluster value for the cluster that runs the
// network itself, as opposed to one tenant namespace.
const PlatformCluster = "platform"

// Safe reports whether every cluster survives the removal.
func Safe(impacts []Impact) bool {
	for _, i := range impacts {
		if i.Refusal != "" {
			return false
		}
	}
	return true
}

// PlanRemoval works out what removing a node costs every raft cluster it is a
// member of: the platform cluster and each namespace allocated to it.
//
// raftNodeID identifies the node in the platform configuration; peerID is what
// the namespace tables key on. They differ on a cluster that predates stable
// raft identities, which is why both are taken rather than derived.
func PlanRemoval(survivor inspector.Node, raftNodeID, peerID string) ([]Impact, error) {
	members, err := RaftMembers(survivor)
	if err != nil {
		return nil, err
	}

	impacts := []Impact{impactFor(PlatformCluster, members, raftNodeID)}

	nsMembers, err := namespaceVoters(survivor)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(nsMembers))
	for name := range nsMembers {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		if !hasMember(nsMembers[name], peerID) {
			continue
		}
		impacts = append(impacts, impactFor(name, nsMembers[name], peerID))
	}
	return impacts, nil
}

// impactFor states the arithmetic for one cluster.
//
// The verdict comes from rqlite.SafeToRemoveMember rather than a second copy of
// the quorum rule. The counts are for the operator to read; the refusal is what
// decides.
func impactFor(cluster string, members []rqlite.RaftMember, target string) Impact {
	imp := Impact{Cluster: cluster, Refusal: rqlite.SafeToRemoveMember(members, target)}
	for _, m := range members {
		if !m.Voter {
			continue
		}
		imp.VotersBefore++
		if m.ID == target {
			continue
		}
		imp.VotersAfter++
		if m.Reachable {
			imp.ReachableAfter++
		}
	}
	imp.QuorumAfter = imp.VotersAfter/2 + 1
	return imp
}

func hasMember(members []rqlite.RaftMember, id string) bool {
	for _, m := range members {
		if m.ID == id {
			return true
		}
	}
	return false
}

// namespaceVotersSQL lists the rqlite voters of every namespace cluster.
//
// A namespace's raft membership is what namespace_cluster_nodes records: one
// running row per node per role, of which the two rqlite roles are the voters.
// Reachability is the node's own liveness in dns_nodes, which is the same
// signal the cluster's reconciler acts on — the alternative, curling each
// namespace's rqlite port on every node, asks the operator's machine to reach
// ports that are bound to the overlay.
const namespaceVotersSQL = `SELECT nc.namespace_name, ncn.node_id, COALESCE(dn.status, '')
	FROM namespace_cluster_nodes ncn
	JOIN namespace_clusters nc ON nc.id = ncn.namespace_cluster_id
	LEFT JOIN dns_nodes dn ON dn.id = ncn.node_id
	WHERE ncn.role IN ('rqlite_leader', 'rqlite_follower') AND ncn.status = 'running'`

// namespaceVoters returns each namespace's rqlite voters, keyed by namespace.
func namespaceVoters(survivor inspector.Node) (map[string][]rqlite.RaftMember, error) {
	out, err := QuerySQL(survivor, namespaceVotersSQL)
	if err != nil {
		return nil, err
	}
	return parseNamespaceVoters(out)
}

// parseNamespaceVoters decodes the query response into per-namespace members.
func parseNamespaceVoters(body []byte) (map[string][]rqlite.RaftMember, error) {
	var resp rqliteResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("parse rqlite response %q: %w", strings.TrimSpace(string(body)), err)
	}
	if resp.Error != "" {
		return nil, fmt.Errorf("rqlite: %s", resp.Error)
	}

	out := map[string][]rqlite.RaftMember{}
	for _, r := range resp.Results {
		if r.Error != "" {
			return nil, fmt.Errorf("rqlite: %s", r.Error)
		}
		for _, row := range r.Values {
			if len(row) < 3 {
				return nil, fmt.Errorf("unexpected namespace_cluster_nodes row shape: %v", row)
			}
			ns := asString(row[0])
			out[ns] = append(out[ns], rqlite.RaftMember{
				ID:        asString(row[1]),
				Voter:     true,
				Reachable: asString(row[2]) == "active",
			})
		}
	}
	return out, nil
}

// FormatImpacts renders the plan as the operator reads it before confirming.
func FormatImpacts(impacts []Impact) string {
	var b strings.Builder
	for _, i := range impacts {
		fmt.Fprintf(&b, "  %-24s %d voters → %d, quorum %d, reachable %d",
			i.Cluster, i.VotersBefore, i.VotersAfter, i.QuorumAfter, i.ReachableAfter)
		if i.Refusal != "" {
			fmt.Fprintf(&b, "  ✗ %s", i.Refusal)
		}
		b.WriteString("\n")
	}
	return b.String()
}
