package rqlite

import (
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
					return fmt.Errorf("node ID mismatch: %s != %s", expectedID, node.ID)
				}
				return nil
			}
		}
		return nil
	}
	return nil
}

