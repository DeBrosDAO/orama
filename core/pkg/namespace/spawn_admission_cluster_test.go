package namespace

import (
	"context"
	"errors"
	"testing"
)

// The hole: acme was deleted and created again under a new cluster id, and the
// provisioner of the first incarnation, still running, sends a spawn. The status
// alone says "provisioning" or "ready" and would admit it into the new cluster.
func TestAdmitSpawn_refusesASpawnForAnEarlierIncarnationOfTheNamespace(t *testing.T) {
	cm, reg := newAdmissionCM(ClusterStatusProvisioning)
	reg.id.Store("c2")

	release, err := cm.AdmitSpawn(context.Background(), "acme", "c1")
	if release != nil || !errors.Is(err, ErrClusterMismatch) {
		t.Fatalf("release=%v err=%v; want ErrClusterMismatch", release != nil, err)
	}
	unlock, lerr := cm.systemdSpawner.LockNamespace(context.Background(), "acme")
	if lerr != nil {
		t.Fatalf("the lock was left held by a refused spawn: %v", lerr)
	}
	unlock()

	release, err = cm.AdmitSpawn(context.Background(), "acme", "c2")
	if err != nil {
		t.Fatalf("a spawn for the current cluster: %v", err)
	}
	release()
}

// A provisioner on the previous release names no cluster: it is admitted while
// the namespace is not being deleted, and refused when it is.
func TestAdmitSpawn_aSpawnWithoutAClusterIdIsCheckedForDeletionOnly(t *testing.T) {
	cm, reg := newAdmissionCM(ClusterStatusReady)
	reg.id.Store("c9")
	release, err := cm.AdmitSpawn(context.Background(), "acme", "")
	if err != nil {
		t.Fatalf("legacy spawn: %v", err)
	}
	release()

	reg.status.Store(ClusterStatusDeprovisioning)
	if _, err := cm.AdmitSpawn(context.Background(), "acme", ""); !errors.Is(err, ErrNamespaceBeingDeleted) {
		t.Fatalf("legacy spawn into a deleted namespace: %v; want ErrNamespaceBeingDeleted", err)
	}
}
