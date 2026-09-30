package namespace

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/DeBrosOfficial/network/pkg/client"
	"go.uber.org/zap"
)

// The orphan sweep is the backstop for teardown (teardown.go).
//
// Teardown removes a namespace from the nodes the coordinator could reach. A
// node that was down at the time, a rollback that could not finish, or a
// namespace deleted by an older release leaves units and state that an
// `orama node upgrade` or a reboot brings back to life: the registry no longer
// knows the namespace, but the node still starts it. Each node therefore checks
// its own disk against the registry and tears down what the registry does not
// assign to it.
//
// Tearing down is destructive, so every doubt means "do nothing":
//
//   - the registry read must have succeeded, and must hold at least one
//     cluster: an empty registry beside a node full of tenants is a fresh or
//     lagging database, not a fleet that deleted every namespace;
//   - a namespace being provisioned by this process is never touched, and its
//     allocation rows, which the registry writes before any node spawns
//     anything, keep it registered on every other node;
//   - an orphan must be seen on orphanSweepsRequired consecutive sweeps, so one
//     inconsistent read cannot delete a namespace;
//   - the node's own instances (index, nameserver, system) are never candidates.

// orphanSweepsRequired is how many consecutive sweeps must find a namespace
// orphaned before it is torn down.
const orphanSweepsRequired = 2

const (
	// registeredNamespacesQuery names the namespaces the registry assigns to
	// this node, through either table that carries the assignment: membership
	// rows, or port allocations (written first, before anything is spawned).
	// Any cluster status counts: a failed cluster's rows were withdrawn by its
	// own rollback, and one still holding rows is left alone.
	registeredNamespacesQuery = `SELECT DISTINCT c.namespace_name AS namespace_name
		FROM namespace_clusters c
		WHERE EXISTS (SELECT 1 FROM namespace_cluster_nodes cn
		               WHERE cn.namespace_cluster_id = c.id AND cn.node_id = ?)
		   OR EXISTS (SELECT 1 FROM namespace_port_allocations pa
		               WHERE pa.namespace_cluster_id = c.id AND pa.node_id = ?)`

	registryClusterCountQuery = `SELECT COUNT(*) AS count FROM namespace_clusters`
)

// registeredNamespaces reads the namespaces the registry assigns to this node
// and how many clusters the registry holds in all.
func (cm *ClusterManager) registeredNamespaces(ctx context.Context) (map[string]bool, int, error) {
	internalCtx := client.WithInternalAuth(ctx)

	var names []struct {
		Name string `db:"namespace_name"`
	}
	if err := cm.db.Query(internalCtx, &names, registeredNamespacesQuery, cm.localNodeID, cm.localNodeID); err != nil {
		return nil, 0, fmt.Errorf("read the namespaces assigned to node %s: %w", cm.localNodeID, err)
	}
	var counts []struct {
		Count int `db:"count"`
	}
	if err := cm.db.Query(internalCtx, &counts, registryClusterCountQuery); err != nil {
		return nil, 0, fmt.Errorf("count the clusters in the registry: %w", err)
	}

	registered := make(map[string]bool, len(names))
	for _, n := range names {
		registered[n.Name] = true
	}
	total := 0
	if len(counts) > 0 {
		total = counts[0].Count
	}
	return registered, total, nil
}

// localTenantNamespaces lists the tenant namespaces with state on this node.
func (cm *ClusterManager) localTenantNamespaces() ([]string, error) {
	if cm.localTenantsFn != nil {
		return cm.localTenantsFn()
	}
	return cm.systemdSpawner.systemdMgr.LocalTenantNamespaces()
}

func (cm *ClusterManager) teardownLocal(ctx context.Context, namespace string) error {
	if cm.teardownLocalFn != nil {
		return cm.teardownLocalFn(ctx, namespace)
	}
	return cm.systemdSpawner.TeardownNamespace(ctx, namespace)
}

// reapOrphanedTenants tears down tenant namespaces that have state on this node
// but that the registry does not assign to it. See the comment at the top of
// this file for what must hold before it acts.
func (cm *ClusterManager) reapOrphanedTenants(ctx context.Context) error {
	local, err := cm.localTenantNamespaces()
	if err != nil {
		cm.confirmOrphans(nil)
		return fmt.Errorf("list this node's tenant namespaces: %w", err)
	}
	if len(local) == 0 {
		cm.confirmOrphans(nil)
		return nil
	}

	// Local first, registry second: a namespace provisioned in between already
	// has its allocation rows, so it reads as registered, never as an orphan.
	registered, clusters, err := cm.registeredNamespaces(ctx)
	if err != nil {
		cm.confirmOrphans(nil)
		return err
	}
	if clusters == 0 {
		cm.confirmOrphans(nil)
		cm.logger.Warn("Orphan sweep skipped: the registry holds no namespace clusters although this node has tenant state; not treating an empty registry as proof that every namespace is gone",
			zap.Strings("local_namespaces", local))
		return nil
	}

	var orphans []string
	for _, ns := range local {
		if !isPlatformNamespace(ns) && !registered[ns] && !cm.isProvisioningLocally(ns) {
			orphans = append(orphans, ns)
		}
	}

	var errs []error
	for _, ns := range cm.confirmOrphans(orphans) {
		cm.logger.Warn("Tearing down a namespace the registry no longer assigns to this node: its units and data would otherwise start again on the next upgrade or reboot",
			zap.String("namespace", ns),
			zap.Int("consecutive_sweeps", orphanSweepsRequired))
		if err := cm.teardownLocal(ctx, ns); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", ns, err))
		}
	}
	return errors.Join(errs...)
}

// confirmOrphans records which namespaces this sweep found orphaned and returns
// those found on orphanSweepsRequired consecutive sweeps. A namespace missing
// from this sweep's list loses its streak, which is why a sweep that could not
// decide passes nil.
func (cm *ClusterManager) confirmOrphans(orphans []string) []string {
	cm.orphanMu.Lock()
	defer cm.orphanMu.Unlock()

	next := make(map[string]int, len(orphans))
	var due []string
	for _, ns := range orphans {
		next[ns] = cm.orphanStreak[ns] + 1
		if next[ns] >= orphanSweepsRequired {
			due = append(due, ns)
		}
	}
	cm.orphanStreak = next
	sort.Strings(due)
	return due
}

// restoreAssigned reports whether a namespace found in this node's local state
// should be restored at boot, acting on the ones that must not be.
//
// The registry may be unreachable this early in boot (this restore exists so
// tenants come up before the index rqlite has a leader), and then the local
// state is all there is: it restores, and says so. When the registry answers
// and assigns this node no part of the cluster, the namespace is gone or this
// node was replaced: it is not restored, and a namespace the registry knows
// nothing about is torn down rather than left for the upgrade to start. An
// empty registry proves nothing and tears nothing down.
func (cm *ClusterManager) restoreAssigned(ctx context.Context, state *ClusterLocalState) (bool, error) {
	var rows []struct {
		Count int `db:"count"`
	}
	const assignedQuery = `SELECT COUNT(*) AS count FROM namespace_cluster_nodes WHERE namespace_cluster_id = ? AND node_id = ?`
	if err := cm.db.Query(ctx, &rows, assignedQuery, state.ClusterID, cm.localNodeID); err != nil || len(rows) == 0 {
		cm.logger.Warn("Cannot verify against the registry that this node still hosts the namespace; restoring it from local state",
			zap.String("namespace", state.NamespaceName), zap.Error(err))
		return true, nil
	}
	if rows[0].Count > 0 {
		return true, nil
	}

	registered, clusters, err := cm.registeredNamespaces(ctx)
	switch {
	case err != nil:
		return false, fmt.Errorf("verify namespace %s against the registry: %w", state.NamespaceName, err)
	case clusters == 0:
		cm.logger.Warn("The registry holds no namespace clusters, so it cannot show that this namespace is gone; not restoring it and not removing it",
			zap.String("namespace", state.NamespaceName))
	case registered[state.NamespaceName]:
		// The namespace was re-created under a new cluster id and this node is
		// part of it: the state on disk belongs to the old incarnation.
		cm.logger.Warn("Local cluster state belongs to a previous incarnation of the namespace; stopping it and dropping the state",
			zap.String("namespace", state.NamespaceName), zap.String("cluster_id", state.ClusterID))
		cm.systemdSpawner.StopAll(ctx, state.NamespaceName)
		if err := os.Remove(filepath.Join(cm.baseDataDir, state.NamespaceName, "cluster-state.json")); err != nil && !os.IsNotExist(err) {
			return false, fmt.Errorf("remove stale cluster state of %s: %w", state.NamespaceName, err)
		}
	default:
		cm.logger.Warn("The registry no longer assigns this namespace to this node; tearing it down instead of restoring it",
			zap.String("namespace", state.NamespaceName), zap.String("cluster_id", state.ClusterID))
		if err := cm.teardownLocal(ctx, state.NamespaceName); err != nil {
			return false, fmt.Errorf("tear down namespace %s: %w", state.NamespaceName, err)
		}
	}
	return false, nil
}
