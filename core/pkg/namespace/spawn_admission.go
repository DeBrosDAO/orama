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
// A spawn names the cluster it is for (clusterID), and is refused with
// ErrClusterMismatch when that is not the namespace's current cluster: a
// provisioner of a deleted incarnation of a re-created name must not start units
// into the new cluster. The cluster id is the registry's, read under the lock.
//
// An empty clusterID is a request from a provisioner on the release before
// cluster ids were sent. It is accepted, checked only for the namespace being
// deleted, for as long as such nodes may exist in the fleet (the same bounded
// mixed-version window as a teardown without one, see TeardownNamespaceOfCluster);
// once every node runs this release it is to be refused as malformed.
//
// A registry that cannot be read refuses too: starting units on a guess is
// what this exists to stop.
func (cm *ClusterManager) AdmitSpawn(ctx context.Context, namespace, clusterID string) (release func(), err error) {
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
	if clusterID != "" && cluster.ID != clusterID {
		unlock()
		return nil, fmt.Errorf("spawn refused for namespace %s: it is for cluster %s and the namespace's cluster is %s: %w",
			namespace, clusterID, cluster.ID, ErrClusterMismatch)
	}
	return unlock, nil
}

// spawnAdmitted runs spawn, a start of one of namespace's units on this node
// for cluster clusterID (provisioning, repair, WebRTC), under AdmitSpawn.
func (cm *ClusterManager) spawnAdmitted(ctx context.Context, namespace, clusterID string, spawn func() error) error {
	release, err := cm.AdmitSpawn(ctx, namespace, clusterID)
	if err != nil {
		return err
	}
	defer release()
	return spawn()
}
