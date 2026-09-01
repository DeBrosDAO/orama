package node

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/DeBrosOfficial/network/pkg/config"
	"github.com/DeBrosOfficial/network/pkg/namespace"
)

func (n *Node) indexSupervisor() (*namespace.IndexSupervisor, string, error) {
	dataDir, err := config.ExpandPath(n.config.Node.DataDir)
	if err != nil {
		return nil, "", err
	}
	nodeID := n.config.Node.ID
	if nodeID == "" {
		nodeID = "node"
	}
	return namespace.NewIndexSupervisor(filepath.Dir(dataDir), n.logger.Logger), nodeID, nil
}

// startIndexWireGuard brings up existing wg0 before libp2p / rqlite need the mesh.
func (n *Node) startIndexWireGuard(_ context.Context) error {
	sup, nodeID, err := n.indexSupervisor()
	if err != nil {
		return err
	}
	return sup.EnsureWireGuard(nodeID)
}

// startIndexStorage starts IPFS, cluster, and the GC timer after cluster config
// files exist and before the index gateway needs the APIs.
func (n *Node) startIndexStorage(_ context.Context) error {
	sup, nodeID, err := n.indexSupervisor()
	if err != nil {
		return err
	}
	if err := sup.EnsureIPFS(nodeID); err != nil {
		return fmt.Errorf("index ipfs: %w", err)
	}
	if err := sup.EnsureIPFSCluster(nodeID); err != nil {
		return fmt.Errorf("index ipfs-cluster: %w", err)
	}
	if err := sup.EnsureIPFSGC(nodeID); err != nil {
		return fmt.Errorf("index ipfs-gc: %w", err)
	}
	return nil
}

// startIndexEdge starts vault, optional SNI router, Caddy, ntfy, and anyone-client
// after the index gateway is on :6001 (Caddy reverse_proxies that port).
func (n *Node) startIndexEdge(_ context.Context) error {
	sup, nodeID, err := n.indexSupervisor()
	if err != nil {
		return err
	}
	if err := sup.EnsureVault(nodeID); err != nil {
		return fmt.Errorf("index vault: %w", err)
	}
	if err := sup.EnsureSNIRouter(nodeID, n.config.SNIRouter.Enabled); err != nil {
		return fmt.Errorf("index sni-router: %w", err)
	}
	if err := sup.EnsureCaddy(nodeID); err != nil {
		return fmt.Errorf("index caddy: %w", err)
	}
	if err := sup.EnsureNtfy(nodeID); err != nil {
		return fmt.Errorf("index ntfy: %w", err)
	}
	if err := sup.EnsureAnyoneClient(nodeID); err != nil {
		return fmt.Errorf("index anyone-client: %w", err)
	}
	return nil
}

// startNameserver starts orama-namespace-coredns@nameserver after index rqlite.
func (n *Node) startNameserver(_ context.Context) error {
	if !n.isNameserverPreference() {
		return nil
	}
	sup, nodeID, err := n.indexSupervisor()
	if err != nil {
		return err
	}
	return sup.EnsureCoreDNS(nodeID)
}
