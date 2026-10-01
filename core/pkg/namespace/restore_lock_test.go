package namespace

import (
	"context"
	"strings"
	"testing"
	"time"

	"go.uber.org/zap"
)

// A namespace's membership has a row per role, so the deprovision listed each
// node three times; torn down concurrently, those three raced on one node's
// units and files.
func TestTeardownNamespaceOnNodes_aNodeListedOncePerRoleIsTornDownOnce(t *testing.T) {
	sw := newStaleSweep(nil, nil, false)
	nodes := append(append(append([]staleClusterNode{}, twoRemoteNodes...), twoRemoteNodes...), twoRemoteNodes...)

	if err := sw.cm.teardownNamespaceOnNodes(context.Background(), nodes, "acme", cleanupScope{}); err != nil {
		t.Fatalf("teardown: %v", err)
	}
	if got := sw.count("stop:teardown-namespace"); got != len(twoRemoteNodes) {
		t.Errorf("%d teardown requests for %d nodes", got, len(twoRemoteNodes))
	}
}

// restoreWith is a cluster manager whose registry holds cluster c1 in status
// and no port allocation, and whose spawner takes namespace locks.
func restoreWith(status ClusterStatus) *ClusterManager {
	sw := newStaleSweep([]NamespaceCluster{{ID: "c1", NamespaceName: "acme", Status: status}}, nil, false)
	sw.cm.systemdSpawner = NewSystemdSpawner("/nonexistent", "", zap.NewNop())
	return sw.cm
}

// The restore read its list of ready clusters before reaching this one; a
// cluster whose delete began since is not restored.
func TestRestoreClusterOnNode_aClusterNoLongerReadyIsNotRestored(t *testing.T) {
	if err := restoreWith(ClusterStatusDeprovisioning).restoreClusterOnNode(context.Background(), "c1", "acme", "10.0.0.1"); err != nil {
		t.Fatalf("restoring a cluster being deleted: %v; want it skipped", err)
	}
	// A ready one goes on to read its port allocation, which this registry
	// does not have.
	err := restoreWith(ClusterStatusReady).restoreClusterOnNode(context.Background(), "c1", "acme", "10.0.0.1")
	if err == nil || !strings.Contains(err.Error(), "no port allocation") {
		t.Fatalf("restoring a ready cluster: %v; want it to proceed to the port allocation", err)
	}
}

// While a teardown holds the namespace's lock the restore waits, and it reads
// the cluster's status only once the teardown is done.
func TestRestoreClusterOnNode_waitsForATeardownOfTheNamespace(t *testing.T) {
	cm := restoreWith(ClusterStatusDeprovisioning)
	unlock := cm.systemdSpawner.LockNamespace("acme")

	done := make(chan error, 1)
	go func() { done <- cm.restoreClusterOnNode(context.Background(), "c1", "acme", "10.0.0.1") }()
	select {
	case err := <-done:
		t.Fatalf("the restore ran while the teardown held the namespace: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	unlock()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("restore after the teardown: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the restore never ran after the teardown released the namespace")
	}
}

// The lock is per namespace: one namespace's teardown does not hold up
// another's restore.
func TestLockNamespace_isPerNamespace(t *testing.T) {
	s := NewSystemdSpawner("/nonexistent", "", zap.NewNop())
	unlockA := s.LockNamespace("a")
	defer unlockA()
	got := make(chan struct{})
	go func() { s.LockNamespace("b")(); close(got) }()
	select {
	case <-got:
	case <-time.After(5 * time.Second):
		t.Fatal("locking namespace b waited for namespace a")
	}
}
