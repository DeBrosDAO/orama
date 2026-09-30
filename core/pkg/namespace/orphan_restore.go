package namespace

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"go.uber.org/zap"
)

// clusterAssignedQuery counts this node's assignment to one cluster id: a
// membership row, or a port allocation. Recovery and provisioning write the
// allocation first and the membership after the node's services are spawned, so
// a node that reboots in between has the allocation only.
const clusterAssignedQuery = `SELECT (
		(SELECT COUNT(*) FROM namespace_cluster_nodes WHERE namespace_cluster_id = ? AND node_id = ?) +
		(SELECT COUNT(*) FROM namespace_port_allocations WHERE namespace_cluster_id = ? AND node_id = ?)
	) AS count`

// restoreAssigned reports whether a namespace found in this node's local state
// should be restored at boot, acting on the ones that must not be.
//
// The registry may be unreachable this early in boot (this restore exists so
// tenants come up before the index rqlite has a leader), and then the local
// state is all there is: it restores, and says so. The same holds for a
// registry that cannot be trusted to have the answer — an empty one, or one
// that disowns every tenant on the node (registryDisownsEveryTenant).
//
// Otherwise the node is judged by cluster id, not by name. A node the registry
// assigns to this cluster — by membership or allocation — restores. A node it
// assigns to another cluster of the same name holds the state of a previous
// incarnation: stopped, its state dropped. A namespace the registry assigns
// this node no part of is torn down rather than left for the upgrade to start,
// at most orphanTeardownsPerPass per boot.
func (cm *ClusterManager) restoreAssigned(ctx context.Context, state *ClusterLocalState) (bool, error) {
	assigned, err := cm.countRows(ctx, clusterAssignedQuery, state.ClusterID, cm.localNodeID, state.ClusterID, cm.localNodeID)
	if err != nil {
		cm.logger.Warn("Cannot verify against the registry that this node still hosts the namespace; restoring it from local state",
			zap.String("namespace", state.NamespaceName), zap.Error(err))
		return true, nil
	}
	if assigned > 0 {
		return true, nil
	}

	registered, clusters, err := cm.registeredNamespaces(ctx)
	switch {
	case err != nil:
		return false, fmt.Errorf("verify namespace %s against the registry: %w", state.NamespaceName, err)
	case clusters == 0:
		cm.logger.Warn("The registry holds no namespace clusters, so it cannot show that this namespace is gone; restoring it from local state",
			zap.String("namespace", state.NamespaceName))
		return true, nil
	case registered[state.NamespaceName]:
		return false, cm.dropPreviousIncarnation(ctx, state)
	}
	return cm.tearDownUnassignedAtBoot(ctx, registered, state)
}

// dropPreviousIncarnation stops the local services of a namespace that was
// re-created under a new cluster id with this node part of it, and removes the
// state that belongs to the old incarnation.
func (cm *ClusterManager) dropPreviousIncarnation(ctx context.Context, state *ClusterLocalState) error {
	cm.logger.Warn("Local cluster state belongs to a previous incarnation of the namespace; stopping it and dropping the state",
		zap.String("namespace", state.NamespaceName), zap.String("cluster_id", state.ClusterID))
	if err := cm.systemdSpawner.StopAll(ctx, state.NamespaceName); err != nil {
		return fmt.Errorf("stop the previous incarnation of %s: %w", state.NamespaceName, err)
	}
	if err := os.Remove(filepath.Join(cm.baseDataDir, state.NamespaceName, "cluster-state.json")); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove stale cluster state of %s: %w", state.NamespaceName, err)
	}
	return nil
}

// tearDownUnassignedAtBoot tears down a namespace the registry assigns this
// node nothing of, unless the registry itself is in doubt. It reports whether
// the namespace should be restored instead.
func (cm *ClusterManager) tearDownUnassignedAtBoot(ctx context.Context, registered map[string]bool, state *ClusterLocalState) (bool, error) {
	local, err := cm.localTenantNamespaces()
	if err != nil {
		cm.logger.Warn("Cannot list this node's tenants to cross-check the registry; restoring the namespace from local state",
			zap.String("namespace", state.NamespaceName), zap.Error(err))
		return true, nil
	}
	if tenants := tenantsOnly(local); registryDisownsEveryTenant(tenants, registered) {
		cm.logger.Error("The registry assigns this node none of its tenant namespaces, which points at a wrong or rolled-back registry; restoring the namespace from local state instead of tearing it down",
			zap.String("namespace", state.NamespaceName), zap.Strings("local_namespaces", tenants))
		return true, nil
	}
	if cm.bootTeardowns >= orphanTeardownsPerPass {
		cm.logger.Error("Boot restore has already torn down the most namespaces it will in one pass; restoring this one from local state. If the registry is right, the orphan sweep removes it",
			zap.String("namespace", state.NamespaceName), zap.Int("limit", orphanTeardownsPerPass))
		return true, nil
	}
	cm.bootTeardowns++
	cm.logger.Warn("The registry no longer assigns this namespace to this node; tearing it down instead of restoring it",
		zap.String("namespace", state.NamespaceName), zap.String("cluster_id", state.ClusterID))
	if err := cm.teardownUnassigned(ctx, state.NamespaceName); err != nil {
		return false, fmt.Errorf("tear down namespace %s: %w", state.NamespaceName, err)
	}
	return false, nil
}
