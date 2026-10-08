package namespace

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

// admissionRegistry is a registry holding one cluster of "acme" whose status the
// test changes, or no cluster, or an error, as the test says.
type admissionRegistry struct {
	status  atomic.Value // ClusterStatus; "" means no cluster
	failure atomic.Value // string; non-empty is the error the registry returns
	id      atomic.Value // string; the id of the cluster the registry holds
}

func newAdmissionCM(initial ClusterStatus) (*ClusterManager, *admissionRegistry) {
	reg := &admissionRegistry{}
	reg.status.Store(initial)
	reg.failure.Store("")
	reg.id.Store("c1")
	db := &recoveryMockDB{}
	db.queryFunc = func(dest any, _ string, _ ...any) error {
		if msg := reg.failure.Load().(string); msg != "" {
			return errors.New(msg)
		}
		if d, ok := dest.(*[]NamespaceCluster); ok {
			if st := reg.status.Load().(ClusterStatus); st != "" {
				*d = []NamespaceCluster{{ID: reg.id.Load().(string), NamespaceName: "acme", Status: st}}
			}
		}
		return nil
	}
	cm := &ClusterManager{
		db:             db,
		logger:         zap.NewNop(),
		localNodeID:    "node-a",
		systemdSpawner: NewSystemdSpawner("/nonexistent", "", zap.NewNop()),
	}
	return cm, reg
}

func TestAdmitSpawn_admitsAClusterThatIsStillServedOrBeingProvisioned(t *testing.T) {
	for _, st := range []ClusterStatus{ClusterStatusProvisioning, ClusterStatusReady, ClusterStatusDegraded} {
		cm, _ := newAdmissionCM(st)
		release, err := cm.AdmitSpawn(context.Background(), "acme", "c1")
		if err != nil {
			t.Fatalf("status %s: %v; want the spawn admitted", st, err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
		if _, lerr := cm.systemdSpawner.LockNamespace(ctx, "acme"); lerr == nil {
			t.Fatalf("status %s: the namespace's lock was free while an admitted spawn held it", st)
		}
		cancel()
		release()
	}
}

// A provisioner whose namespace was deleted mid-provision must not start units
// after the teardown: the cluster is 'deprovisioning', or already gone.
func TestAdmitSpawn_refusesANamespaceBeingDeletedOrGone(t *testing.T) {
	for _, st := range []ClusterStatus{ClusterStatusDeprovisioning, ""} {
		cm, _ := newAdmissionCM(st)
		release, err := cm.AdmitSpawn(context.Background(), "acme", "c1")
		if release != nil || !errors.Is(err, ErrNamespaceBeingDeleted) {
			t.Fatalf("status %q: release=%v err=%v; want ErrNamespaceBeingDeleted", st, release != nil, err)
		}
		// A refusal must not leave the lock held.
		unlock, lerr := cm.systemdSpawner.LockNamespace(context.Background(), "acme")
		if lerr != nil {
			t.Fatalf("status %q: the lock was left held by a refused spawn: %v", st, lerr)
		}
		unlock()
	}
}

// The race itself: the spawn is in flight while the teardown holds the lock, and
// the cluster is marked 'deprovisioning' by then. Without the status read under
// the lock the spawn runs the moment the teardown lets go.
func TestAdmitSpawn_readsTheStatusOnlyOnceTheTeardownHasLetGo(t *testing.T) {
	cm, reg := newAdmissionCM(ClusterStatusReady)
	teardown, err := cm.systemdSpawner.LockNamespace(context.Background(), "acme")
	if err != nil {
		t.Fatal(err)
	}

	spawned := false
	done := make(chan error, 1)
	go func() {
		done <- cm.spawnAdmitted(context.Background(), "acme", "c1", func() error { spawned = true; return nil })
	}()
	select {
	case err := <-done:
		t.Fatalf("the spawn ran while the teardown held the namespace: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	reg.status.Store(ClusterStatusDeprovisioning)
	teardown()

	select {
	case err := <-done:
		if !errors.Is(err, ErrNamespaceBeingDeleted) {
			t.Fatalf("spawn after the teardown: %v; want ErrNamespaceBeingDeleted", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the spawn never returned after the teardown released the namespace")
	}
	if spawned {
		t.Fatal("units were started for a namespace being deleted")
	}
}

func TestAdmitSpawn_aRegistryThatCannotBeReadRefuses(t *testing.T) {
	cm, reg := newAdmissionCM(ClusterStatusReady)
	reg.failure.Store("no leader")
	spawned := false
	err := cm.spawnAdmitted(context.Background(), "acme", "c1", func() error { spawned = true; return nil })
	if err == nil || errors.Is(err, ErrNamespaceBeingDeleted) {
		t.Fatalf("err = %v; want the registry's error, not a verdict on the namespace", err)
	}
	if spawned {
		t.Fatal("a spawn was made on a guess")
	}
}

func TestAdmitSpawn_aWaiterWhoseContextEndsIsRefused(t *testing.T) {
	cm, _ := newAdmissionCM(ClusterStatusReady)
	teardown, err := cm.systemdSpawner.LockNamespace(context.Background(), "acme")
	if err != nil {
		t.Fatal(err)
	}
	defer teardown()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	spawned := false
	err = cm.spawnAdmitted(ctx, "acme", "c1", func() error { spawned = true; return nil })
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v; want context.DeadlineExceeded wrapped", err)
	}
	if spawned {
		t.Fatal("the spawn ran without the lock")
	}
}

func TestSpawnAdmitted_returnsTheSpawnsError(t *testing.T) {
	cm, _ := newAdmissionCM(ClusterStatusProvisioning)
	want := errors.New("systemctl start failed")
	if err := cm.spawnAdmitted(context.Background(), "acme", "c1", func() error { return want }); !errors.Is(err, want) {
		t.Fatalf("err = %v; want the spawn's error", err)
	}
}

// restoreFromDisk writes a cluster-state.json for acme and returns a manager
// that restores from it, with the log it writes.
func restoreFromDisk(t *testing.T, clusterID string) (*ClusterManager, *observer.ObservedLogs, string) {
	t.Helper()
	base := t.TempDir()
	core, logs := observer.New(zap.InfoLevel)
	lg := zap.New(core)
	cm := &ClusterManager{
		logger:         lg,
		localNodeID:    "node-a",
		baseDataDir:    base,
		systemdSpawner: NewSystemdSpawner(base, "", lg),
	}
	path := writeClusterState(t, base, clusterID)
	return cm, logs, path
}

func writeClusterState(t *testing.T, base, clusterID string) string {
	t.Helper()
	dir := filepath.Join(base, "acme")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(ClusterLocalState{ClusterID: clusterID, NamespaceName: "acme"})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "cluster-state.json")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// The disk restore read the state, then waited for a delete's teardown, which
// removes the state: it must not start the units of what was deleted.
func TestRestoreClusterFromState_aTeardownWhileItWaitedRemovedTheState(t *testing.T) {
	cm, logs, path := restoreFromDisk(t, "c1")
	teardown, err := cm.systemdSpawner.LockNamespace(context.Background(), "acme")
	if err != nil {
		t.Fatal(err)
	}

	done := make(chan error, 1)
	go func() {
		done <- cm.restoreClusterFromState(context.Background(), &ClusterLocalState{ClusterID: "c1", NamespaceName: "acme"})
	}()
	select {
	case err := <-done:
		t.Fatalf("the restore ran while the teardown held the namespace: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	teardown()

	if err := <-done; err != nil {
		t.Fatalf("restore after a teardown: %v; want it skipped", err)
	}
	if logs.FilterMessageSnippet("its state was removed while the restore waited").Len() != 1 {
		t.Fatalf("the restore did not skip: %v", logs.All())
	}
	if logs.FilterMessageSnippet("Cannot determine whether rqlite is running").Len() != 0 {
		t.Fatal("the restore went on to start units of a deleted namespace")
	}
}

func TestRestoreClusterFromState_aNamespaceRecreatedWhileItWaitedIsNotRestoredFromTheOldState(t *testing.T) {
	cm, logs, _ := restoreFromDisk(t, "c2")
	err := cm.restoreClusterFromState(context.Background(), &ClusterLocalState{ClusterID: "c1", NamespaceName: "acme"})
	if err != nil {
		t.Fatal(err)
	}
	if logs.FilterMessageSnippet("re-created while the restore waited").Len() != 1 {
		t.Fatalf("the restore did not skip: %v", logs.All())
	}
}

func TestRestoreClusterFromState_anUnchangedStateIsRestored(t *testing.T) {
	cm, logs, _ := restoreFromDisk(t, "c1")
	if err := cm.restoreClusterFromState(context.Background(), &ClusterLocalState{ClusterID: "c1", NamespaceName: "acme"}); err != nil {
		t.Fatal(err)
	}
	if logs.FilterMessageSnippet("Not restoring a namespace from local state").Len() != 0 {
		t.Fatalf("a namespace whose state is intact was skipped: %v", logs.All())
	}
	if logs.FilterMessageSnippet("Cannot determine whether rqlite is running").Len() != 1 {
		t.Fatalf("the restore did not reach the units: %v", logs.All())
	}
}

func TestRestoreClusterFromState_aLockThatStaysHeldFailsTheRestore(t *testing.T) {
	cm, _, _ := restoreFromDisk(t, "c1")
	holder, err := cm.systemdSpawner.LockNamespace(context.Background(), "acme")
	if err != nil {
		t.Fatal(err)
	}
	defer holder()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	err = cm.restoreClusterFromState(ctx, &ClusterLocalState{ClusterID: "c1", NamespaceName: "acme"})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v; want context.DeadlineExceeded wrapped", err)
	}
}
