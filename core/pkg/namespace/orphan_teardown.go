package namespace

import (
	"context"
	"errors"
	"fmt"
	"slices"
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
//   - the node's own instances (index, nameserver, system) are never candidates;
//   - a registry that disowns EVERY tenant on the node is a registry problem (a
//     database restored from an older snapshot, a node pointed at another
//     cluster's), not a fleet that deleted them all: nothing is torn down;
//   - at most orphanTeardownsPerPass namespaces are torn down in one pass, so a
//     registry that is wrong about a few still cannot wipe the node at once.

// orphanSweepsRequired is how many consecutive sweeps must find a namespace
// orphaned before it is torn down.
const orphanSweepsRequired = 2

const (
	// orphanTeardownsPerPass caps the namespaces one sweep, or one boot
	// restore, tears down.
	orphanTeardownsPerPass = 2

	// orphanGuardMinTenants is how many tenants a node must hold for "the
	// registry disowns all of them" to mean anything: a node with one tenant
	// cannot tell a deleted namespace from a wrong registry, and is protected
	// by the two-sweep rule and the cap alone.
	orphanGuardMinTenants = 2
)

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

	// namespaceClustersQuery counts the clusters of one namespace in the
	// registry, on any node.
	namespaceClustersQuery = `SELECT COUNT(*) AS count FROM namespace_clusters WHERE namespace_name = ?`
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

// teardownLocal tears a namespace down on this node; purgeData also removes its
// tenant data (TeardownNamespaceAndData).
func (cm *ClusterManager) teardownLocal(ctx context.Context, namespace string, purgeData bool) error {
	if cm.teardownLocalFn != nil {
		return cm.teardownLocalFn(ctx, namespace, purgeData)
	}
	if purgeData {
		return cm.systemdSpawner.TeardownNamespaceAndData(ctx, namespace)
	}
	return cm.systemdSpawner.TeardownNamespace(ctx, namespace)
}

// registryDisownsEveryTenant reports whether none of the node's tenants is
// registered to it, on a node that holds enough of them for that to be
// suspicious (orphanGuardMinTenants).
func registryDisownsEveryTenant(tenants []string, registered map[string]bool) bool {
	if len(tenants) < orphanGuardMinTenants {
		return false
	}
	for _, ns := range tenants {
		if registered[ns] {
			return false
		}
	}
	return true
}

// tenantsOnly drops the node's own instances from a list of namespaces.
func tenantsOnly(local []string) []string {
	var tenants []string
	for _, ns := range local {
		if !isPlatformNamespace(ns) {
			tenants = append(tenants, ns)
		}
	}
	return tenants
}

// teardownUnassigned tears down a namespace the registry does not assign to
// this node. When the registry holds no cluster of that name at all the
// namespace was deleted, and its tenant data goes with it; when the namespace
// lives on other nodes this node only stops hosting it, and its data is left.
func (cm *ClusterManager) teardownUnassigned(ctx context.Context, namespace string) error {
	clusters, err := cm.countRows(ctx, namespaceClustersQuery, namespace)
	if err != nil {
		return fmt.Errorf("check whether the registry still knows namespace %s: %w", namespace, err)
	}
	return cm.teardownLocal(ctx, namespace, clusters == 0)
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
	tenants := tenantsOnly(local)
	if len(tenants) == 0 {
		cm.confirmOrphans(nil)
		cm.setRegistryDisowned(nil)
		cm.pruneTeardownAttempts(nil)
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
		cm.setRegistryDisowned(tenants)
		cm.logger.Warn("Orphan sweep skipped: the registry holds no namespace clusters although this node has tenant state; not treating an empty registry as proof that every namespace is gone",
			zap.Strings("local_namespaces", tenants))
		return nil
	}
	if registryDisownsEveryTenant(tenants, registered) {
		cm.confirmOrphans(nil)
		cm.setRegistryDisowned(tenants)
		cm.logger.Error("Orphan sweep skipped: the registry assigns this node none of its tenant namespaces. That points at a wrong or rolled-back registry (an older snapshot, another cluster's database), not at every namespace having been deleted",
			zap.Strings("local_namespaces", tenants), zap.Int("registry_clusters", clusters))
		return nil
	}

	cm.setRegistryDisowned(nil)
	var orphans []string
	for _, ns := range tenants {
		if !registered[ns] && !cm.isProvisioningLocally(ns) {
			orphans = append(orphans, ns)
		}
	}
	cm.pruneTeardownAttempts(orphans)
	return cm.teardownOrphans(ctx, cm.confirmOrphans(orphans))
}

// teardownOrphans tears down the namespaces that have been orphaned for long
// enough, at most orphanTeardownsPerPass of them. The rest keep their streak
// and are taken on a later sweep.
func (cm *ClusterManager) teardownOrphans(ctx context.Context, due []string) error {
	due = cm.leastRecentlyAttempted(due)
	if len(due) > orphanTeardownsPerPass {
		cm.logger.Warn("More namespaces are orphaned than one sweep tears down; the rest wait for the next sweep",
			zap.Int("orphaned", len(due)), zap.Int("per_sweep", orphanTeardownsPerPass))
		due = due[:orphanTeardownsPerPass]
	}
	var errs []error
	for _, ns := range due {
		cm.logger.Warn("Tearing down a namespace the registry no longer assigns to this node: its units and data would otherwise start again on the next upgrade or reboot",
			zap.String("namespace", ns),
			zap.Int("consecutive_sweeps", orphanSweepsRequired))
		err := cm.teardownUnassigned(ctx, ns)
		cm.recordTeardown(ns, err)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", ns, err))
		}
	}
	return errors.Join(errs...)
}

// leastRecentlyAttempted orders namespaces due for teardown by when their last
// failed attempt was: those never attempted first, then the one attempted
// longest ago, ties alphabetical. The per-pass cap takes a prefix of this list,
// so however many teardowns keep failing, each due namespace reaches the front
// in turn instead of the same few holding every slot.
func (cm *ClusterManager) leastRecentlyAttempted(due []string) []string {
	cm.orphanMu.Lock()
	defer cm.orphanMu.Unlock()
	ordered := append([]string(nil), due...)
	sort.SliceStable(ordered, func(i, j int) bool {
		return cm.teardownAttempt[ordered[i]] < cm.teardownAttempt[ordered[j]]
	})
	return ordered
}

// recordTeardown remembers when the teardown of ns last failed, and forgets ns
// once it succeeded.
func (cm *ClusterManager) recordTeardown(ns string, err error) {
	cm.orphanMu.Lock()
	defer cm.orphanMu.Unlock()
	if err == nil {
		delete(cm.teardownAttempt, ns)
		return
	}
	if cm.teardownAttempt == nil {
		cm.teardownAttempt = make(map[string]uint64)
	}
	cm.teardownSeq++
	cm.teardownAttempt[ns] = cm.teardownSeq
}

// pruneTeardownAttempts forgets the attempts of namespaces that are no longer
// orphaned: a namespace that was registered again or is gone from the disk has
// nothing left to retry.
func (cm *ClusterManager) pruneTeardownAttempts(orphans []string) {
	cm.orphanMu.Lock()
	defer cm.orphanMu.Unlock()
	for ns := range cm.teardownAttempt {
		if !slices.Contains(orphans, ns) {
			delete(cm.teardownAttempt, ns)
		}
	}
}

// setRegistryDisowned records the outcome of a sweep's check of the registry
// against the node's tenants: the tenants it disowns entirely, or nil.
func (cm *ClusterManager) setRegistryDisowned(tenants []string) {
	cm.orphanMu.Lock()
	defer cm.orphanMu.Unlock()
	if len(tenants) == 0 {
		cm.disownedTenants, cm.disownedStreak = nil, 0
		return
	}
	cm.disownedTenants = append([]string(nil), tenants...)
	cm.disownedStreak++
}

// RegistryDisownedTenants returns the tenant namespaces on this node while the
// registry has disowned every one of them — or held no cluster at all — on
// orphanSweepsRequired consecutive sweeps, else nil. The orphan sweep and the boot restore do nothing in that
// state, so it is surfaced to the operator through the node's telemetry report
// (a monitor alert) instead of only a log line every minute.
func (cm *ClusterManager) RegistryDisownedTenants() []string {
	cm.orphanMu.Lock()
	defer cm.orphanMu.Unlock()
	if cm.disownedStreak < orphanSweepsRequired {
		return nil
	}
	return append([]string(nil), cm.disownedTenants...)
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
