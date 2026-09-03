package rqlite

import (
	"errors"
	"fmt"
	"time"
)

// SetDiscoveryService sets the cluster discovery service
func (r *RQLiteManager) SetDiscoveryService(service *ClusterDiscoveryService) {
	r.discoveryService = service
}

// SetNodeType sets the node type
func (r *RQLiteManager) SetNodeType(nodeType string) {
	if nodeType != "" {
		r.nodeType = nodeType
	}
}

// UpdateAdvertisedAddresses overrides advertised addresses
func (r *RQLiteManager) UpdateAdvertisedAddresses(raftAddr, httpAddr string) {
	if r == nil || r.discoverConfig == nil {
		return
	}
	if raftAddr != "" && r.discoverConfig.RaftAdvAddress != raftAddr {
		r.discoverConfig.RaftAdvAddress = raftAddr
	}
	if httpAddr != "" && r.discoverConfig.HttpAdvAddress != httpAddr {
		r.discoverConfig.HttpAdvAddress = httpAddr
	}
}

// ErrNodeIDMismatch means this node appears in the raft configuration under an
// id that is not its current advertise address.
//
// Today the raft node id IS the advertise address, so a mismatch means the
// address changed under a member that raft still knows by the old one — a WG
// re-provision, a node replacement, a 10.0.0.x reassignment. Left alone, orphan
// recovery adds the new address as a SECOND voter and the old entry stays: two
// such events on a 5-voter cluster leave 5 live voters needing 4 of 7 to agree.
//
// Surfacing it is all this can do until the raft id is decoupled from the
// address (chg-302) and something is able to retire the stale entry (bug-301).
var ErrNodeIDMismatch = errors.New("rqlite node id does not match this node's advertise address")

func (r *RQLiteManager) validateNodeID() error {
	for i := 0; i < 5; i++ {
		nodes, err := r.getRQLiteNodes()
		if err != nil {
			if i < 4 {
				time.Sleep(500 * time.Millisecond)
				continue
			}
			return nil
		}

		expectedID := r.discoverConfig.RaftAdvAddress
		if expectedID == "" || len(nodes) == 0 {
			return nil
		}

		for _, node := range nodes {
			if node.Address == expectedID {
				if node.ID != expectedID {
					return fmt.Errorf("%w: raft knows %s as id %q", ErrNodeIDMismatch, node.Address, node.ID)
				}
				return nil
			}
		}
		return nil
	}
	return nil
}
