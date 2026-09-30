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
	purged     map[string]bool // namespaces torn down with their tenant data
	teardownEr error
	assigned   int // rows of the restore self-check's count
	// nsClusters is how many clusters of the namespace the registry holds; the
	// default 0 means the namespace was deleted.
	nsClusters int
	queries    []string
}

func newOrphanHarness(local, registered []string, clusters int) *orphanHarness {
	h := &orphanHarness{local: local, registered: registered, clusters: clusters, assigned: 1, purged: map[string]bool{}}
	db := &recoveryMockDB{queryFunc: func(dest any, query string, _ ...any) error {
		if h.readErr != nil {
			return h.readErr
		}
		h.queries = append(h.queries, query)
		switch query {
		case namespaceClustersQuery:
			h.setCount(dest, h.nsClusters)
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
		teardownLocalFn: func(_ context.Context, ns string, purge bool) error {
			if h.teardownEr != nil {
				return h.teardownEr
			}
			h.tornDown = append(h.tornDown, ns)
			h.purged[ns] = purge
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

// T5: an empty registry proves nothing, so the tenants that are on the node
// come back from their local state rather than staying down.
func TestRestoreAssigned_anEmptyRegistryRestoresFromLocalState(t *testing.T) {
	h := newOrphanHarness(nil, nil, 0)
	h.assigned = 0

	proceed, err := h.cm.restoreAssigned(context.Background(), &ClusterLocalState{ClusterID: "c1", NamespaceName: "acme"})
	if err != nil || !proceed || len(h.tornDown) != 0 {
		t.Fatalf("proceed = %v, err = %v, torn down = %v; want a restore and no teardown", proceed, err, h.tornDown)
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

// T2: a node with an allocation for THIS cluster id and no membership row yet
// (recovery writes the membership after the spawn) is joining the namespace,
// not holding a previous incarnation of it. The judgement is by cluster id.
func TestRestoreAssigned_anAllocationForThisClusterIDIsAnAssignment(t *testing.T) {
	h := newOrphanHarness([]string{"acme", "x"}, []string{"acme"}, 3)
	var gotArgs []any
	h.cm.db = &recoveryMockDB{queryFunc: func(dest any, query string, args ...any) error {
		if query == clusterAssignedQuery {
			gotArgs = args
			h.setCount(dest, 1) // the allocation row, no membership row
		}
		return nil
	}}

	proceed, err := h.cm.restoreAssigned(context.Background(), &ClusterLocalState{ClusterID: "c-new", NamespaceName: "acme"})
	if err != nil || !proceed || len(h.tornDown) != 0 {
		t.Fatalf("proceed = %v, err = %v, torn down = %v", proceed, err, h.tornDown)
	}
	if !reflect.DeepEqual(gotArgs, []any{"c-new", "local", "c-new", "local"}) {
		t.Fatalf("assignment checked with %v, want the cluster id (not the name) against membership and allocation", gotArgs)
	}
}

// The query itself, against SQLite: allocation only, membership only, another
// cluster's rows, and another node's rows.
func TestClusterAssignedQuery_countsMembershipOrAllocationOfThatClusterID(t *testing.T) {
	reg := newRegistryRig(t)
	db := reg.db
	for _, id := range []string{"c-alloc", "c-member", "c-other-node"} {
		reg.cluster(id, "acme-"+id)
	}
	reg.allocation("c-alloc", "n1")
	reg.membership("c-member", "n1")
	reg.allocation("c-other-node", "n2")

	count := func(cluster, node string) int {
		var n int
		if err := db.QueryRow(clusterAssignedQuery, cluster, node, cluster, node).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	for _, tc := range []struct {
		cluster, node string
		want          int
	}{
		{"c-alloc", "n1", 1}, {"c-member", "n1", 1}, {"c-other-node", "n1", 0}, {"c-gone", "n1", 0}, {"c-alloc", "n2", 0},
	} {
		if got := count(tc.cluster, tc.node); got != tc.want {
			t.Errorf("count(%s, %s) = %d, want %d", tc.cluster, tc.node, got, tc.want)
		}
	}
}

// T3: a registry that disowns every tenant on the node is wrong, not a fleet
// that deleted them all.
func TestReapOrphanedTenants_aRegistryThatDisownsEveryTenantTearsNothingDown(t *testing.T) {
	h := newOrphanHarness([]string{"a", "b", "c"}, []string{"elsewhere"}, 3)

	for i := 0; i < 4; i++ {
		if err := h.sweep(t); err != nil {
			t.Fatal(err)
		}
	}
	if len(h.tornDown) != 0 {
		t.Fatalf("torn down = %v from a registry that knows none of this node's tenants", h.tornDown)
	}
}

func TestReapOrphanedTenants_oneRegisteredTenantMakesTheRegistryCredible(t *testing.T) {
	h := newOrphanHarness([]string{"a", "kept"}, []string{"kept"}, 3)
	_ = h.sweep(t)
	_ = h.sweep(t)
	if !reflect.DeepEqual(h.tornDown, []string{"a"}) {
		t.Fatalf("torn down = %v", h.tornDown)
	}
}

// T3: at most orphanTeardownsPerPass per sweep; the rest keep their streak and
// go on the next sweep.
func TestReapOrphanedTenants_capsTheTeardownsPerSweep(t *testing.T) {
	h := newOrphanHarness([]string{"kept", "a", "b", "c", "d", "e"}, []string{"kept"}, 3)
	_ = h.sweep(t)
	_ = h.sweep(t)
	if len(h.tornDown) != orphanTeardownsPerPass {
		t.Fatalf("torn down = %v after one confirming sweep, want %d", h.tornDown, orphanTeardownsPerPass)
	}
	_ = h.sweep(t)
	_ = h.sweep(t)
	sort.Strings(h.tornDown)
	if len(h.tornDown) < 2*orphanTeardownsPerPass {
		t.Fatalf("torn down = %v, the remainder must follow on later sweeps", h.tornDown)
	}
}

// Boot restore is guarded the same way: a wrong registry restores from local
// state instead of tearing tenants down, and the cap bounds one boot.
func TestRestoreAssigned_aRegistryThatDisownsEveryTenantRestoresFromLocalState(t *testing.T) {
	h := newOrphanHarness([]string{"a", "b"}, []string{"elsewhere"}, 3)
	h.assigned = 0

	proceed, err := h.cm.restoreAssigned(context.Background(), &ClusterLocalState{ClusterID: "c", NamespaceName: "a"})
	if err != nil || !proceed || len(h.tornDown) != 0 {
		t.Fatalf("proceed = %v, err = %v, torn down = %v", proceed, err, h.tornDown)
	}
}

func TestRestoreAssigned_capsTheTeardownsOfOneBoot(t *testing.T) {
	h := newOrphanHarness([]string{"kept", "a", "b", "c"}, []string{"kept"}, 3)
	h.assigned = 0

	var restored []string
	for _, ns := range []string{"a", "b", "c"} {
		proceed, err := h.cm.restoreAssigned(context.Background(), &ClusterLocalState{ClusterID: "c-" + ns, NamespaceName: ns})
		if err != nil {
			t.Fatal(err)
		}
		if proceed {
			restored = append(restored, ns)
		}
	}
	if len(h.tornDown) != orphanTeardownsPerPass || len(restored) != 1 {
		t.Fatalf("torn down = %v, restored from local state = %v", h.tornDown, restored)
	}
}

// T6: the data goes when the registry holds no cluster of that name at all (the
// namespace was deleted), and stays when the namespace lives on other nodes.
func TestReapOrphanedTenants_purgesTenantDataOnlyForADeletedNamespace(t *testing.T) {
	h := newOrphanHarness([]string{"deleted", "moved", "kept"}, []string{"kept"}, 3)
	base := h.cm.db.(*recoveryMockDB).queryFunc
	h.cm.db.(*recoveryMockDB).queryFunc = func(dest any, query string, args ...any) error {
		if query == namespaceClustersQuery {
			n := 0
			if args[0] == "moved" {
				n = 1
			}
			h.setCount(dest, n)
			return nil
		}
		return base(dest, query, args...)
	}
	_ = h.sweep(t)
	_ = h.sweep(t)

	if !h.purged["deleted"] || h.purged["moved"] {
		t.Fatalf("purged = %v, want data removed for the deleted namespace only", h.purged)
	}
}

// The reviewer's note: the registry reads of the sweep and of the boot restore
// go through cm.db, the registry handle, which reads at level=weak (the leader)
// — never through a node-local handle, which can be a stale follower.
func TestOrphanRegistryReads_goThroughTheRegistryHandle(t *testing.T) {
	h := newOrphanHarness([]string{"a", "kept"}, []string{"kept"}, 3)
	h.assigned = 0
	_ = h.sweep(t)
	_, _ = h.cm.restoreAssigned(context.Background(), &ClusterLocalState{ClusterID: "c", NamespaceName: "a"})

	db := h.cm.db.(*recoveryMockDB)
	seen := map[string]bool{}
	for _, c := range db.queryCalls {
		seen[c.Query] = true
	}
	for _, q := range []string{registeredNamespacesQuery, registryClusterCountQuery, clusterAssignedQuery} {
		if !seen[q] {
			t.Errorf("registry read not issued through cm.db: %.60s", q)
		}
	}
}
