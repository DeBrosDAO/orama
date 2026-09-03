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

	// CreatedAt is when the join handshake wrote the row.
	CreatedAt time.Time

	// ConfirmedAt is when the node behind this row was first seen in
	// dns_nodes, and is zero until then.
	//
	// It is a latch, not a liveness signal: once a node has proved it came up,
	// its row is never dropped for being unconfirmed, only for the node having
	// departed. That asymmetry is what makes it safe to garbage-collect failed
	// joins without any risk of severing a live node from the mesh.
	ConfirmedAt time.Time
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

	// OrphanWireGuardPeers are CONFIRMED rows whose overlay address matches no
	// dns_nodes row. They are REPORTED, never dropped — see BuildPlan.
	OrphanWireGuardPeers []string

	// ConfirmWireGuardPeers are node_ids of rows whose node has now been seen
	// in dns_nodes, and which should have confirmed_at set.
	ConfirmWireGuardPeers []string

	// DropUnconfirmedWireGuardPeers are node_ids of rows written by a join
	// that never completed: no node ever appeared at that address, and the
	// join window has long since passed.
	DropUnconfirmedWireGuardPeers []string
}

// Empty reports whether the plan would change nothing.
func (p Plan) Empty() bool {
	return len(p.DropWireGuardPeers) == 0 &&
		len(p.DropDNSNodes) == 0 &&
		len(p.ConfirmWireGuardPeers) == 0 &&
		len(p.DropUnconfirmedWireGuardPeers) == 0
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

	// JoinGrace is how long an unconfirmed wireguard_peers row is left alone
	// before it is treated as the residue of a join that never finished.
	//
	// A real join takes minutes — install, first boot, raft join, DNS
	// registration — so this is several times the longest plausible one. The
	// only thing it costs a genuinely slow joiner is having to run join again,
	// and the row it protects is the one that used to be re-applied to every
	// survivor's interface every 60 seconds indefinitely.
	JoinGrace = 30 * time.Minute
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
// therefore done on the overlay address.
//
// A row that still finds no match is split on whether the node behind it was
// ever confirmed. Never confirmed and older than JoinGrace is a join that did
// not finish, and is dropped — that residue is the whole reason this rule
// exists. Never confirmed but recent is a join still in flight, since a node
// gets its WireGuard row before its dns_nodes row. Confirmed is a node that
// came up and then vanished from dns_nodes, which is only reported: it is an
// inconsistency in a store this package does not own, and deleting the mesh
// entry of a machine that may well still be running would sever it.
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
		confirmed := !row.ConfirmedAt.IsZero()

		if !known {
			switch {
			case confirmed:
				// A node that demonstrably came up and is no longer in
				// dns_nodes is an anomaly worth an operator's attention, not
				// something to clean up silently.
				plan.OrphanWireGuardPeers = append(plan.OrphanWireGuardPeers, row.NodeID)
			case row.CreatedAt.IsZero():
				// No usable creation time, so there is no way to tell a join
				// still in flight from residue. Keeping it is the safe
				// direction, but keeping it silently is not: this is exactly
				// the ghost class the rule exists to surface.
				plan.OrphanWireGuardPeers = append(plan.OrphanWireGuardPeers, row.NodeID)
			case e.Now.Sub(row.CreatedAt) >= JoinGrace:
				plan.DropUnconfirmedWireGuardPeers = append(plan.DropUnconfirmedWireGuardPeers, row.NodeID)
			}
			// An unconfirmed row still inside the join window belongs to a
			// join that may yet succeed. Leave it.
			continue
		}

		if !confirmed {
			// The node appeared. Latch it, so its row is never again a
			// candidate for the rule above.
			plan.ConfirmWireGuardPeers = append(plan.ConfirmWireGuardPeers, row.NodeID)
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
	sort.Strings(plan.ConfirmWireGuardPeers)
	sort.Strings(plan.DropUnconfirmedWireGuardPeers)
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
