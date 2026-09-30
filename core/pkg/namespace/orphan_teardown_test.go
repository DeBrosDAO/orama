package namespace

import (
	"context"
	"errors"
	"reflect"
	"sort"
	"testing"

	"go.uber.org/zap"
)

// orphanHarness is a ClusterManager whose registry answers with `registered`
// (the namespaces assigned to this node) out of `clusters` in all, and whose
// node holds `local` on disk. Teardowns are recorded.
type orphanHarness struct {
	cm         *ClusterManager
	local      []string
	registered []string
	clusters   int
	readErr    error
	listErr    error
	tornDown   []string
	teardownEr error
	assigned   int // rows of the restore self-check's count
}

func newOrphanHarness(local, registered []string, clusters int) *orphanHarness {
	h := &orphanHarness{local: local, registered: registered, clusters: clusters, assigned: 1}
	db := &recoveryMockDB{queryFunc: func(dest any, query string, _ ...any) error {
		if h.readErr != nil {
			return h.readErr
		}
		switch query {
		case registeredNamespacesQuery:
			rows := reflect.ValueOf(dest).Elem()
			for _, ns := range h.registered {
				row := reflect.New(rows.Type().Elem()).Elem()
				row.Field(0).SetString(ns)
				rows.Set(reflect.Append(rows, row))
			}
		case registryClusterCountQuery:
			h.setCount(dest, h.clusters)
		default:
			h.setCount(dest, h.assigned)
		}
		return nil
	}}
	h.cm = &ClusterManager{
		db:           db,
		logger:       zap.NewNop(),
		localNodeID:  "local",
		provisioning: map[string]bool{},
		localTenantsFn: func() ([]string, error) {
			return h.local, h.listErr
		},
		teardownLocalFn: func(_ context.Context, ns string) error {
			if h.teardownEr != nil {
				return h.teardownEr
			}
			h.tornDown = append(h.tornDown, ns)
			return nil
		},
	}
	return h
}

func (h *orphanHarness) setCount(dest any, n int) {
	rows := reflect.ValueOf(dest).Elem()
	row := reflect.New(rows.Type().Elem()).Elem()
	row.Field(0).SetInt(int64(n))
	rows.Set(reflect.Append(rows, row))
}

func (h *orphanHarness) sweep(t *testing.T) error {
	t.Helper()
	return h.cm.reapOrphanedTenants(context.Background())
}

// The stagenet shape: the registry no longer knows the namespace, the node
// still has its units and data. One sweep is not proof; two are.
func TestReapOrphanedTenants_tearsDownAnOrphanSeenTwiceAndLeavesTheRegisteredOne(t *testing.T) {
	h := newOrphanHarness([]string{"gone", "kept"}, []string{"kept"}, 3)

	if err := h.sweep(t); err != nil || len(h.tornDown) != 0 {
		t.Fatalf("first sweep: err = %v, torn down = %v; an orphan must be seen twice", err, h.tornDown)
	}
	if err := h.sweep(t); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(h.tornDown, []string{"gone"}) {
		t.Fatalf("torn down = %v, want only the orphan", h.tornDown)
	}
}

// A namespace that was orphaned once and then registered again (a re-create, or
// one inconsistent read) loses its streak.
func TestReapOrphanedTenants_aStreakBrokenByARegistrationStartsOver(t *testing.T) {
	h := newOrphanHarness([]string{"flaky"}, nil, 3)
	_ = h.sweep(t)

	h.registered = []string{"flaky"}
	_ = h.sweep(t)
	h.registered = nil
	_ = h.sweep(t)
	if len(h.tornDown) != 0 {
		t.Fatalf("torn down = %v after only one consecutive orphan sweep", h.tornDown)
	}
	_ = h.sweep(t)
	if !reflect.DeepEqual(h.tornDown, []string{"flaky"}) {
		t.Fatalf("torn down = %v, want flaky after two consecutive sweeps", h.tornDown)
	}
}

func TestReapOrphanedTenants_doesNothingWhenTheRegistryCannotBeRead(t *testing.T) {
	h := newOrphanHarness([]string{"gone"}, nil, 3)
	_ = h.sweep(t) // streak of one

	h.readErr = errors.New("rqlite unavailable")
	if err := h.sweep(t); err == nil {
		t.Fatal("a failed registry read was not reported")
	}
	h.readErr = nil
	_ = h.sweep(t)
	if len(h.tornDown) != 0 {
		t.Fatalf("torn down = %v: a failed read must break the consecutive streak, not count as a sweep", h.tornDown)
	}
}

// An empty registry next to a node full of tenants is a fresh or lagging
// database, not a fleet that deleted everything.
func TestReapOrphanedTenants_anEmptyRegistryProvesNothing(t *testing.T) {
	h := newOrphanHarness([]string{"a", "b"}, nil, 0)

	for i := 0; i < 3; i++ {
		if err := h.sweep(t); err != nil {
			t.Fatal(err)
		}
	}
	if len(h.tornDown) != 0 {
		t.Fatalf("torn down = %v from an empty registry", h.tornDown)
	}
}

func TestReapOrphanedTenants_skipsANamespaceProvisioningOnThisNode(t *testing.T) {
	h := newOrphanHarness([]string{"new"}, nil, 3)
	h.cm.provisioning["new"] = true

	_ = h.sweep(t)
	_ = h.sweep(t)
	if len(h.tornDown) != 0 {
		t.Fatalf("torn down = %v while it was being provisioned", h.tornDown)
	}
}

func TestReapOrphanedTenants_neverTouchesThePlatformNamespaces(t *testing.T) {
	h := newOrphanHarness([]string{"index", "nameserver", "system"}, nil, 3)

	_ = h.sweep(t)
	_ = h.sweep(t)
	if len(h.tornDown) != 0 {
		t.Fatalf("torn down = %v", h.tornDown)
	}
}

func TestReapOrphanedTenants_aFailedListingDoesNothing(t *testing.T) {
	h := newOrphanHarness([]string{"gone"}, nil, 3)
	h.listErr = errors.New("systemctl unavailable")

	if err := h.sweep(t); err == nil {
		t.Fatal("a failed listing was not reported")
	}
	if len(h.tornDown) != 0 {
		t.Fatalf("torn down = %v", h.tornDown)
	}
}

func TestReapOrphanedTenants_nothingOnDisk(t *testing.T) {
	h := newOrphanHarness(nil, nil, 0)
	if err := h.sweep(t); err != nil || len(h.tornDown) != 0 {
		t.Fatalf("err = %v, torn down = %v", err, h.tornDown)
	}
}

// A teardown that failed is retried by the next sweep: the streak is kept.
func TestReapOrphanedTenants_aFailedTeardownIsRetriedNextSweep(t *testing.T) {
	h := newOrphanHarness([]string{"gone"}, nil, 3)
	_ = h.sweep(t)
	h.teardownEr = errors.New("disable failed")
	if err := h.sweep(t); err == nil {
		t.Fatal("a failed teardown was not reported")
	}
	h.teardownEr = nil
	if err := h.sweep(t); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(h.tornDown, []string{"gone"}) {
		t.Fatalf("torn down = %v, want the retry to succeed", h.tornDown)
	}
}

func TestReapOrphanedTenants_severalOrphansAreAllTornDown(t *testing.T) {
	h := newOrphanHarness([]string{"b", "a", "kept"}, []string{"kept"}, 3)
	_ = h.sweep(t)
	_ = h.sweep(t)
	sort.Strings(h.tornDown)
	if !reflect.DeepEqual(h.tornDown, []string{"a", "b"}) {
		t.Fatalf("torn down = %v", h.tornDown)
	}
}

// Boot restore: a namespace the registry no longer assigns to this node is not
// restored, and one the registry has never heard of is torn down rather than
// left for the next upgrade to start.
func TestRestoreAssigned_skipsAndTearsDownANamespaceAbsentFromTheRegistry(t *testing.T) {
	h := newOrphanHarness(nil, []string{"other"}, 3)
	h.assigned = 0
	state := &ClusterLocalState{ClusterID: "c-old", NamespaceName: "gone"}

	proceed, err := h.cm.restoreAssigned(context.Background(), state)
	if err != nil || proceed {
		t.Fatalf("proceed = %v, err = %v; a deleted namespace must not be restored", proceed, err)
	}
	if !reflect.DeepEqual(h.tornDown, []string{"gone"}) {
		t.Fatalf("torn down = %v, want the deleted namespace torn down", h.tornDown)
	}
}

func TestRestoreAssigned_restoresANamespaceThisNodeStillHosts(t *testing.T) {
	h := newOrphanHarness(nil, []string{"acme"}, 3)
	h.assigned = 1

	proceed, err := h.cm.restoreAssigned(context.Background(), &ClusterLocalState{ClusterID: "c1", NamespaceName: "acme"})
	if err != nil || !proceed || len(h.tornDown) != 0 {
		t.Fatalf("proceed = %v, err = %v, torn down = %v", proceed, err, h.tornDown)
	}
}

// At boot the index rqlite may not answer yet; the local state is all there is.
func TestRestoreAssigned_restoresFromLocalStateWhenTheRegistryCannotBeRead(t *testing.T) {
	h := newOrphanHarness(nil, nil, 3)
	h.readErr = errors.New("no leader yet")

	proceed, err := h.cm.restoreAssigned(context.Background(), &ClusterLocalState{ClusterID: "c1", NamespaceName: "acme"})
	if err != nil || !proceed || len(h.tornDown) != 0 {
		t.Fatalf("proceed = %v, err = %v, torn down = %v", proceed, err, h.tornDown)
	}
}

func TestRestoreAssigned_anEmptyRegistryTearsNothingDown(t *testing.T) {
	h := newOrphanHarness(nil, nil, 0)
	h.assigned = 0

	proceed, err := h.cm.restoreAssigned(context.Background(), &ClusterLocalState{ClusterID: "c1", NamespaceName: "acme"})
	if err != nil || proceed || len(h.tornDown) != 0 {
		t.Fatalf("proceed = %v, err = %v, torn down = %v; want no restore and no teardown", proceed, err, h.tornDown)
	}
}

func TestRestoreAssigned_aFailedTeardownIsAnError(t *testing.T) {
	h := newOrphanHarness(nil, []string{"other"}, 3)
	h.assigned = 0
	h.teardownEr = errors.New("disable failed")

	proceed, err := h.cm.restoreAssigned(context.Background(), &ClusterLocalState{ClusterID: "c-old", NamespaceName: "gone"})
	if err == nil || proceed {
		t.Fatalf("proceed = %v, err = %v; want the failure reported", proceed, err)
	}
}
