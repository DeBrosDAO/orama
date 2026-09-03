package node

import (
	"context"
	"fmt"

	"github.com/DeBrosOfficial/network/pkg/node/membership"
	"github.com/DeBrosOfficial/network/pkg/rqlite"
)

// discoveryPeers adapts the cluster discovery service to the reconciler's view
// of who is alive.
type discoveryPeers struct {
	node *Node
}

// LivePeerIDs returns the peer ids libp2p can currently see, including this
// node's own — the leader is by definition alive, and discovery does not list
// itself.
func (d discoveryPeers) LivePeerIDs() map[string]struct{} {
	live := map[string]struct{}{}
	if self := d.node.GetPeerID(); self != "" {
		live[self] = struct{}{}
	}

	cd := d.node.getClusterDiscovery()
	if cd == nil {
		return live
	}
	for _, peer := range cd.GetAllPeers() {
		if peer != nil && peer.PeerID != "" {
			live[peer.PeerID] = struct{}{}
		}
	}
	return live
}

// startMembershipReconciler brings up the loop that keeps dns_nodes and
// wireguard_peers in step with what the cluster believes about its members.
//
// Cluster-tier: every write it makes is cluster-wide, and it only acts on the
// raft leader.
func (n *Node) startMembershipReconciler(ctx context.Context) error {
	adapter := n.getRQLiteAdapter()
	if adapter == nil {
		return fmt.Errorf("membership reconciler needs the rqlite adapter")
	}

	n.membershipOnce.Do(func() {
		r := membership.NewReconciler(
			adapter.GetSQLDB(),
			discoveryPeers{node: n},
			n.isRQLiteLeader,
			n.logger.Logger,
		)
		go r.Run(ctx)
	})
	return nil
}

// isRQLiteLeader reports whether this node's index rqlite is the raft leader.
func (n *Node) isRQLiteLeader() bool {
	status, err := rqlite.GetRaftStatus(n.config.Database.RQLitePort)
	if err != nil {
		return false
	}
	return status.Store.Raft.State == "Leader"
}
