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
	return namespace.NewIndexSupervisor(filepath.Dir(dataDir), n.logger.Logger), n.nodeID(), nil
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

// startIndexEdgeServing starts the units that make this node able to answer
// public traffic: the vault guardian, the optional SNI router, and Caddy, which
// terminates TLS and reverse_proxies the index gateway.
//
// These are separated from the auxiliary units below because registering in
// dns_nodes is a promise that this node serves. Sending traffic to a node whose
// Caddy never started is a fail-open, and gating the registration on ntfy —
// which serves nothing — would be a fail-closed for no reason.
func (n *Node) startIndexEdgeServing(_ context.Context) error {
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
	return nil
}

// startIndexEdgeAux starts ntfy and the anyone-client. Neither terminates
// traffic for this node, so a failure here degrades it without taking it out of
// DNS.
func (n *Node) startIndexEdgeAux(_ context.Context) error {
	sup, nodeID, err := n.indexSupervisor()
	if err != nil {
		return err
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
