// Package membership reconciles the several places a node's existence is
// recorded, so that one machine going away is forgotten everywhere rather than
// in some stores and not others.
//
// Node membership is projected into five stores — dns_nodes, wireguard_peers,
// the index raft configuration, ipfs-cluster's peer list and IPFS's peering
// config — each with its own liveness definition and its own timer, and no
// single writer. A machine deleted without ceremony therefore left a different
// residue in each: an `inactive` dns_nodes row that is never deleted, a
// wireguard_peers row re-applied to the interface every 60 seconds for ever, a
// raft voter still counted toward quorum, and peer addresses still dialled.
//
// This package computes what the membership SHOULD be from evidence the cluster
// already agrees on, diffs it against what each store actually holds, and
// applies one change at a time under a precondition of its own.
package membership

import (
	"sort"
	"time"
)

// Node is one machine as dns_nodes records it. The id is the libp2p peer id —
// the only identifier every store can be keyed back to.
type Node struct {
	PeerID     string
	InternalIP string // WireGuard overlay address
	Status     string // active, draining, offline
	LastSeen   time.Time
}

// WireGuardRow is one row of wireguard_peers.
//
// NodeID is deliberately not assumed to be a peer id. The join handler writes a
// synthetic "node-<wgip>", so it matches no dns_nodes row and never has; the
// reliable join between the two tables is the overlay address, which is the
// machine's actual identity on the mesh.
type WireGuardRow struct {
	NodeID    string
	WGIP      string
	PublicKey string
}

// Plan is the set of changes that would bring the stores in line with the
// desired membership. Every field is sorted, so a plan is comparable and a log
// line is stable.
type Plan struct {
	// DropWireGuardPeers are wireguard_peers.node_id values for machines that
	// are gone for good.
	DropWireGuardPeers []string

	// DropDNSNodes are dns_nodes.id values for machines that are gone for good
	// and past their grace period.
	DropDNSNodes []string

	// OrphanWireGuardPeers are rows whose overlay address matches no dns_nodes
	// row at all. They are REPORTED, never dropped — see BuildPlan.
	OrphanWireGuardPeers []string
}

// Empty reports whether the plan would change nothing.
func (p Plan) Empty() bool {
	return len(p.DropWireGuardPeers) == 0 && len(p.DropDNSNodes) == 0
}

// Grace periods. Both are deliberately long: the cost of forgetting a machine
// that was only briefly unreachable is that it has to rejoin, while the cost of
// remembering a dead one is bounded and already visible.
const (
	// LivenessGrace is how recently a node must have been seen for its rows to
	// be protected regardless of anything else. It is the backstop against
	// acting on a stale evidence read.
	LivenessGrace = 30 * time.Minute

	// TombstoneGrace is how long a tombstoned node keeps its dns_nodes row
	// after eviction, so an operator looking at the table right after a node
	// disappears still sees what was removed and when.
	TombstoneGrace = 6 * time.Hour
)

// Evidence is what the leader knows about the fleet when it builds a plan.
type Evidence struct {
	// Nodes is every dns_nodes row.
	Nodes []Node

	// WireGuardRows is every wireguard_peers row.
	WireGuardRows []WireGuardRow

	// Tombstoned holds the peer ids of nodes removed from raft on purpose,
	// with the time of removal.
	Tombstoned map[string]time.Time

	// Discovered holds the peer ids libp2p discovery can currently see. A node
	// in here is alive whatever the other evidence says.
	Discovered map[string]struct{}

	// Now is the reference time, injected so a plan is reproducible.
	Now time.Time
}

// BuildPlan diffs the desired membership against what the stores hold.
//
// The rules are conservative in one direction on purpose. Removing a row for a
// machine that is actually alive costs an outage — a WireGuard peer deleted
// from a live node severs it from the mesh, and raft runs over the mesh — while
// leaving a row for a machine that is dead costs a stale entry that the next
// cycle will catch. So every deletion needs positive evidence of departure, and
// any single piece of evidence that the node is alive vetoes it.
//
// Orphaned WireGuard rows are the sharp edge. The obvious rule — "delete rows
// with no matching dns_nodes id" — would today delete EVERY row, because the
// join handler writes a synthetic node id that matches nothing. Matching is
// therefore done on the overlay address, and a row that still finds no match is
// only reported: a node that is mid-join has its WireGuard row before its
// dns_nodes row, so absence is not departure.
func BuildPlan(e Evidence) Plan {
	byIP := make(map[string]Node, len(e.Nodes))
	byPeer := make(map[string]Node, len(e.Nodes))
	for _, n := range e.Nodes {
		if n.InternalIP != "" {
			byIP[n.InternalIP] = n
		}
		byPeer[n.PeerID] = n
	}

	var plan Plan

	for _, row := range e.WireGuardRows {
		node, known := byIP[row.WGIP]
		if !known {
			plan.OrphanWireGuardPeers = append(plan.OrphanWireGuardPeers, row.NodeID)
			continue
		}
		if departed(node, e) {
			plan.DropWireGuardPeers = append(plan.DropWireGuardPeers, row.NodeID)
		}
	}

	for _, n := range e.Nodes {
		if !departed(n, e) {
			continue
		}
		evictedAt, tombstoned := e.Tombstoned[n.PeerID]
		if !tombstoned {
			// A dns_nodes row is the record of what the cluster believed. It is
			// only deleted once something deliberate has happened to the node,
			// so an unexplained disappearance stays visible.
			continue
		}
		if e.Now.Sub(evictedAt) < TombstoneGrace {
			continue
		}
		plan.DropDNSNodes = append(plan.DropDNSNodes, n.PeerID)
	}

	sort.Strings(plan.DropWireGuardPeers)
	sort.Strings(plan.DropDNSNodes)
	sort.Strings(plan.OrphanWireGuardPeers)
	return plan
}

// departed reports whether a node is gone for good.
//
// Every clause below is a veto in favour of keeping the node: discovery can
// still see it, it was seen recently, or nothing has said it is gone. Only a
// node that fails all of them is treated as departed.
func departed(n Node, e Evidence) bool {
	if _, live := e.Discovered[n.PeerID]; live {
		return false
	}
	if !n.LastSeen.IsZero() && e.Now.Sub(n.LastSeen) < LivenessGrace {
		return false
	}
	if _, tombstoned := e.Tombstoned[n.PeerID]; tombstoned {
		return true
	}
	// Not tombstoned and not seen: the node is missing, but nothing has
	// established that it is gone rather than briefly unreachable. The raft
	// eviction path is what turns the second into the first, and it writes a
	// tombstone when it does.
	return false
}
