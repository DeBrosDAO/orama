package namespace

import (
	"context"
	"errors"
	"fmt"
)

// ErrNamespaceBeingDeleted marks a spawn refused because the registry says the
// namespace's cluster is being deprovisioned, or is gone.
var ErrNamespaceBeingDeleted = errors.New("the namespace is being deleted")

// AdmitSpawn decides whether this node may start a unit of namespace, and
// returns the namespace's lock, which the caller holds until its spawn is done.
//
// Deleting a namespace and provisioning it are not coordinated across nodes:
// the delete runs wherever the request landed, the provisioner on whichever
// node took the create, and a spawn request reaches a third. The delete marks
// the cluster 'deprovisioning' before it stops anything, and a teardown holds
// the namespace's lock on a node while it stops that node's units. A spawn
// that read the registry before the mark, or was simply in flight, used to
// land after the teardown and leave units holding ports the registry had
// already handed to the next namespace. Taking the lock first and reading the
// cluster under it makes a spawn either wholly before a node's teardown (which
// then stops what it started) or wholly after it (refused here). The first
// refused spawn fails the provisioning, which rolls itself back.
//
// A registry that cannot be read refuses too: starting units on a guess is
// what this exists to stop.
func (cm *ClusterManager) AdmitSpawn(ctx context.Context, namespace string) (release func(), err error) {
	unlock, err := cm.systemdSpawner.LockNamespace(ctx, namespace)
	if err != nil {
		return nil, fmt.Errorf("spawn refused for namespace %s: %w", namespace, err)
	}
	cluster, err := cm.GetClusterByNamespace(ctx, namespace)
	if err != nil {
		unlock()
		return nil, fmt.Errorf("spawn refused for namespace %s: read its cluster from the registry: %w", namespace, err)
	}
	if cluster == nil || cluster.Status == ClusterStatusDeprovisioning {
		unlock()
		return nil, fmt.Errorf("spawn refused for namespace %s: %w", namespace, ErrNamespaceBeingDeleted)
	}
	return unlock, nil
}

// spawnAdmitted runs spawn, a start of one of namespace's units on this node
// by the provisioner, under AdmitSpawn.
func (cm *ClusterManager) spawnAdmitted(ctx context.Context, namespace string, spawn func() error) error {
	release, err := cm.AdmitSpawn(ctx, namespace)
	if err != nil {
		return err
	}
	defer release()
	return spawn()
}
