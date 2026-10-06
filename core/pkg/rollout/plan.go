// Package rollout turns a set of nodes into an ordered, printable upgrade plan.
//
// The rolling upgrade used to be a loop over the order nodes happened to appear
// in nodes.conf, separated by a fixed sleep. docs/DEV_DEPLOY.md claimed
// followers-first, leader-last; nothing in the code implemented it. Restarting
// the leader first costs an election on every node after it, and restarting the
// only two healthy nameservers back to back takes the zone offline.
package rollout

import (
	"fmt"
	"sort"
	"strings"

	"github.com/DeBrosOfficial/network/pkg/inspector"
)

// RaftRole is what a node is doing in the raft configuration right now.
type RaftRole string

const (
	RoleLeader   RaftRole = "Leader"
	RoleFollower RaftRole = "Follower"

	// RoleUnknown is a node whose state could not be read. It is NOT treated
	// as a follower: a node that cannot be reached may be down, and upgrading
	// on the assumption that it is a healthy follower is how a rollout removes
	// the second-to-last voter.
	RoleUnknown RaftRole = "unknown"
)

// Step is one node's place in the plan.
type Step struct {
	Node         inspector.Node
	Role         RaftRole
	IsNameserver bool

	// Reason explains this node's position, for the printed plan. An operator
	// approving a rollout should be able to see why the order is the order.
	Reason string
}

// Plan is the ordered rollout.
type Plan struct {
	Steps []Step

	// Nameservers is how many nameservers the environment has, which decides
	// how many may be down at once.
	Nameservers int
}

// ErrNoLeader means no node reported itself leader.
//
// Fatal rather than "proceed in file order": no leader means the cluster has no
// quorum, and a rolling upgrade is the last thing that should be started
// against a cluster that cannot commit a write.
type ErrNoLeader struct{ Unknown int }

func (e *ErrNoLeader) Error() string {
	if e.Unknown > 0 {
		return fmt.Sprintf("no node reports itself leader (%d node(s) unreachable) — "+
			"the cluster has no quorum, or this host cannot reach it; not starting a rolling upgrade", e.Unknown)
	}
	return "no node reports itself leader — the cluster has no quorum; not starting a rolling upgrade"
}

// ErrUnreachable means a node's raft state could not be read.
type ErrUnreachable struct{ Hosts []string }

func (e *ErrUnreachable) Error() string {
	return fmt.Sprintf("could not read the raft state of %s — a rolling upgrade must not proceed "+
		"on nodes whose health is unknown", strings.Join(e.Hosts, ", "))
}

// Build orders nodes for a rolling upgrade.
//
// Rules, in order of precedence:
//
//  1. The leader goes last. Every restart before it is a follower restart,
//     which costs no election; restarting the leader first costs one election
//     per remaining node.
//  2. Nameservers are spread through the followers rather than run
//     consecutively, so the zone always has a nameserver answering. With three
//     nameservers and one down for its restart, two still serve.
//  3. Within those constraints the order is stable (sorted by host), so a plan
//     printed twice is the same plan.
func Build(nodes []inspector.Node, roles map[string]RaftRole) (*Plan, error) {
	var unreachable []string
	for _, n := range nodes {
		if roles[n.Host] == RoleUnknown || roles[n.Host] == "" {
			unreachable = append(unreachable, n.Host)
		}
	}
	if len(unreachable) > 0 {
		sort.Strings(unreachable)
		return nil, &ErrUnreachable{Hosts: unreachable}
	}

	var leader *inspector.Node
	var followers []inspector.Node
	for i := range nodes {
		if roles[nodes[i].Host] == RoleLeader {
			// More than one leader means the reads straddled an election.
			// Refusing is right: the plan would be built on a state that no
			// longer exists.
			if leader != nil {
				return nil, fmt.Errorf("two nodes report themselves leader (%s and %s) — "+
					"the cluster is mid-election; re-run once it settles", leader.Host, nodes[i].Host)
			}
			leader = &nodes[i]
			continue
		}
		followers = append(followers, nodes[i])
	}
	if leader == nil {
		return nil, &ErrNoLeader{}
	}

	sort.Slice(followers, func(i, j int) bool { return followers[i].Host < followers[j].Host })

	plan := &Plan{Nameservers: countNameservers(nodes)}
	for _, n := range interleaveNameservers(followers) {
		reason := "follower"
		if n.IsNameserver() {
			reason = "follower (nameserver — spaced so the zone keeps answering)"
		}
		plan.Steps = append(plan.Steps, Step{
			Node: n, Role: RoleFollower, IsNameserver: n.IsNameserver(), Reason: reason,
		})
	}
	plan.Steps = append(plan.Steps, Step{
		Node:         *leader,
		Role:         RoleLeader,
		IsNameserver: leader.IsNameserver(),
		Reason:       "leader — last, after leadership transfer",
	})
	return plan, nil
}

// interleaveNameservers alternates nameservers and workers so no two
// nameservers restart back to back.
//
// With only nameservers in the environment — which is the current 3+3 topology
// — there is nothing to interleave with, and the ordering falls back to stable
// host order. That is still correct: they restart one at a time, and each is
// gated on the previous one becoming ready before the next begins.
func interleaveNameservers(nodes []inspector.Node) []inspector.Node {
	var ns, others []inspector.Node
	for _, n := range nodes {
		if n.IsNameserver() {
			ns = append(ns, n)
		} else {
			others = append(others, n)
		}
	}
	if len(ns) == 0 || len(others) == 0 {
		return nodes
	}

	out := make([]inspector.Node, 0, len(nodes))
	for i := 0; i < len(ns) || i < len(others); i++ {
		if i < len(ns) {
			out = append(out, ns[i])
		}
		if i < len(others) {
			out = append(out, others[i])
		}
	}
	return out
}

func countNameservers(nodes []inspector.Node) int {
	n := 0
	for _, node := range nodes {
		if node.IsNameserver() {
			n++
		}
	}
	return n
}

// String renders the plan for an operator to approve.
func (p *Plan) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "Rolling upgrade plan (%d nodes, %d nameservers):\n\n", len(p.Steps), p.Nameservers)
	for i, s := range p.Steps {
		fmt.Fprintf(&b, "  %d. %-16s %-22s %s\n", i+1, s.Node.Host, s.Node.Role, s.Reason)
	}
	b.WriteString("\nEach node is upgraded only after the previous one reports Leader or Follower,\n")
	b.WriteString("an applied index caught up to the leader, and a gateway serving /health.\n")
	return b.String()
}
