package namespace

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/pkg/systemd"
	_ "github.com/mattn/go-sqlite3"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

// guardedDB is a recoveryMockDB whose status-guarded UPDATE reports no row when
// another node has already moved the cluster out of 'provisioning'.
type guardedDB struct {
	*recoveryMockDB
	lostRace bool
}

func (g *guardedDB) Exec(ctx context.Context, query string, args ...any) (sql.Result, error) {
	res, err := g.recoveryMockDB.Exec(ctx, query, args...)
	if err == nil && g.lostRace && strings.Contains(query, "AND status = 'provisioning'") {
		return mockResult{rowsAffected: 0}, nil
	}
	return res, err
}

// staleSweep wires a ClusterManager whose registry reports `stale` as the
// clusters past the threshold and `nodes` as their port-block holders. Every
// write and every remote stop is appended, in order, to events.
type staleSweep struct {
	cm     *ClusterManager
	db     *guardedDB
	mu     sync.Mutex
	events []string
	// stopErr, when it returns an error for an action, fails that remote stop.
	stopErr func(action string) error
	modif   []any
}

func newStaleSweep(stale []NamespaceCluster, nodes []staleClusterNode, lostRace bool) *staleSweep {
	sw := &staleSweep{}
	mock := &recoveryMockDB{
		queryFunc: func(dest any, query string, args ...any) error {
			switch d := dest.(type) {
			case *[]NamespaceCluster:
				sw.modif = args
				*d = stale
			case *[]staleClusterNode:
				*d = nodes
			}
			return nil
		},
		execFunc: func(query string, _ ...any) error {
			sw.record("exec:" + firstWords(query))
			return nil
		},
	}
	sw.db = &guardedDB{recoveryMockDB: mock, lostRace: lostRace}
	lg := zap.NewNop()
	sw.cm = &ClusterManager{
		db:            sw.db,
		logger:        lg,
		localNodeID:   "local",
		portAllocator: NewNamespacePortAllocator(sw.db, lg),
		dnsManager:    NewDNSRecordManager(sw.db, "example.test", lg),
		provisioning:  map[string]bool{},
		spawnRequestFn: func(_ context.Context, _ string, req map[string]interface{}) (*spawnResponse, error) {
			action, _ := req["action"].(string)
			sw.record("stop:" + action)
			if sw.stopErr != nil {
				if err := sw.stopErr(action); err != nil {
					return nil, err
				}
			}
			return &spawnResponse{Success: true}, nil
		},
	}
	return sw
}

func firstWords(q string) string {
	f := strings.Fields(q)
	if len(f) > 4 {
		f = f[:4]
	}
	return strings.Join(f, " ")
}

func (sw *staleSweep) record(e string) {
	sw.mu.Lock()
	defer sw.mu.Unlock()
	sw.events = append(sw.events, e)
}

func (sw *staleSweep) index(prefix string) int {
	for i, e := range sw.events {
		if strings.HasPrefix(e, prefix) {
			return i
		}
	}
	return -1
}

func (sw *staleSweep) count(prefix string) int {
	n := 0
	for _, e := range sw.events {
		if strings.HasPrefix(e, prefix) {
			n++
		}
	}
	return n
}

func countExecs(db *recoveryMockDB, substr string) int {
	n := 0
	for _, c := range db.execCalls {
		if strings.Contains(c.Query, substr) {
			n++
		}
	}
	return n
}

var (
	oneStaleCluster = []NamespaceCluster{{ID: "c1", NamespaceName: "stale", ProvisionedAt: time.Now().Add(-time.Hour)}}
	twoRemoteNodes  = []staleClusterNode{{NodeID: "n1", InternalIP: "10.0.0.1"}, {NodeID: "n2", InternalIP: "10.0.0.2"}}
)

func TestFailStaleProvisioning_stops_services_before_releasing_ports(t *testing.T) {
	sw := newStaleSweep(oneStaleCluster, twoRemoteNodes, false)

	if err := sw.cm.failStaleProvisioning(context.Background()); err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if got := sw.count("stop:teardown-namespace"); got != 2 || sw.count("stop:") != 2 {
		t.Fatalf("teardowns = %d, want exactly one per node, and no plain stop that leaves units enabled (events %v)", got, sw.events)
	}
	lastStop := -1
	for i, e := range sw.events {
		if strings.HasPrefix(e, "stop:") {
			lastStop = i
		}
	}
	release := sw.index("exec:DELETE FROM namespace_port_allocations")
	if release == -1 || lastStop > release {
		t.Fatalf("ports must be released only after every stop: %v", sw.events)
	}
	if u := sw.index("exec:UPDATE namespace_clusters SET"); u < lastStop || u > release {
		t.Fatalf("order must be stop, guarded UPDATE, release: %v", sw.events)
	}
	if countExecs(sw.db.recoveryMockDB, "DELETE FROM dns_records") == 0 {
		t.Fatal("DNS records were not withdrawn")
	}
}

func TestFailStaleProvisioning_failed_remote_stop_is_recorded_and_ports_stay_until_it_succeeds(t *testing.T) {
	sw := newStaleSweep(oneStaleCluster, twoRemoteNodes, false)
	sw.stopErr = func(action string) error {
		if action == teardownAction {
			return errors.New("node unreachable")
		}
		return nil
	}

	err := sw.cm.failStaleProvisioning(context.Background())
	if err == nil || !strings.Contains(err.Error(), "node unreachable") {
		t.Fatalf("a failed stop must be reported, got %v", err)
	}
	if countExecs(sw.db.recoveryMockDB, "INSERT INTO namespace_pending_cleanup") == 0 {
		t.Fatal("the failed remote stop was not recorded for replay")
	}
	if countExecs(sw.db.recoveryMockDB, "SET status = 'failed'") != 0 || countExecs(sw.db.recoveryMockDB, "DELETE FROM namespace_port_allocations") != 0 {
		t.Fatal("ports were released (or the cluster failed) while a service may still be running")
	}

	// The next sweep, once the node answers, finishes the job.
	sw.stopErr = nil
	if err := sw.cm.failStaleProvisioning(context.Background()); err != nil {
		t.Fatalf("second sweep: %v", err)
	}
	if countExecs(sw.db.recoveryMockDB, "SET status = 'failed'") != 1 || countExecs(sw.db.recoveryMockDB, "DELETE FROM namespace_port_allocations") != 1 {
		t.Fatal("the cluster was not failed and released once its services stopped")
	}
}

func TestFailStaleProvisioning_asks_the_registry_for_the_threshold_age(t *testing.T) {
	sw := newStaleSweep(nil, nil, false)
	if err := sw.cm.failStaleProvisioning(context.Background()); err != nil {
		t.Fatalf("sweep: %v", err)
	}
	want := fmt.Sprintf("-%d seconds", int(staleProvisioningAfter.Seconds()))
	if len(sw.modif) != 1 || sw.modif[0] != want {
		t.Fatalf("age modifier = %v, want %q", sw.modif, want)
	}
	if len(sw.db.execCalls) != 0 {
		t.Fatalf("nothing is stale, but %d writes ran", len(sw.db.execCalls))
	}
}

func TestStaleProvisioningAfter_exceeds_the_whole_failure_path(t *testing.T) {
	if floor := provisioningTimeout + rollbackTimeout + markFailedTimeout; staleProvisioningAfter <= floor {
		t.Fatalf("staleProvisioningAfter %v must strictly exceed provisioning+rollback+mark-failed = %v", staleProvisioningAfter, floor)
	}
}

func TestFailStaleProvisioning_leaves_in_flight_clusters_alone(t *testing.T) {
	sw := newStaleSweep([]NamespaceCluster{{ID: "inflight", NamespaceName: "inflight"}}, twoRemoteNodes, false)
	sw.cm.provisioning["inflight"] = true

	if err := sw.cm.failStaleProvisioning(context.Background()); err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if len(sw.db.execCalls) != 0 || sw.count("stop:") != 0 {
		t.Fatalf("an in-flight cluster must not be touched: %v", sw.events)
	}
}

func TestFailStaleProvisioning_losing_the_race_releases_nothing(t *testing.T) {
	sw := newStaleSweep(oneStaleCluster, twoRemoteNodes, true)

	if err := sw.cm.failStaleProvisioning(context.Background()); err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if countExecs(sw.db.recoveryMockDB, "DELETE FROM namespace_port_allocations") != 0 {
		t.Fatal("the node that lost the guarded UPDATE must not release ports")
	}
}

func TestFailStaleProvisioning_reports_unreadable_provisioned_at_and_skips_it(t *testing.T) {
	sw := newStaleSweep(nil, nil, false)
	core, logs := observer.New(zap.ErrorLevel)
	sw.cm.logger = zap.New(core)
	sw.db.queryFunc = func(dest any, query string, _ ...any) error {
		if query == unreadableProvisionedAtQuery {
			v := reflect.ValueOf(dest).Elem()
			row := reflect.New(v.Type().Elem()).Elem()
			row.FieldByName("ID").SetString("bad")
			row.FieldByName("NamespaceName").SetString("badns")
			v.Set(reflect.Append(v, row))
		}
		return nil
	}

	if err := sw.cm.failStaleProvisioning(context.Background()); err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if logs.FilterField(zap.String("cluster_id", "bad")).Len() != 1 {
		t.Fatalf("the unreadable cluster was not reported: %v", logs.All())
	}
	if len(sw.db.execCalls) != 0 {
		t.Fatal("a cluster whose age cannot be judged must not be failed")
	}
}

// The SQL predicate, run against SQLite: the registry's clock decides, stored
// offsets are honoured, and an unparseable value is never "stale".
func TestStaleProvisioningPredicate_against_sqlite(t *testing.T) {
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE namespace_clusters (id TEXT, status TEXT, provisioned_at TIMESTAMP)`); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	old := now.Add(-2 * staleProvisioningAfter)
	rows := map[string]any{
		"old-rfc3339":      old.UTC().Format(time.RFC3339),
		"old-nanos-offset": old.In(time.FixedZone("x", 5*3600)).Format(time.RFC3339Nano),
		"old-sqlite":       old.UTC().Format("2006-01-02 15:04:05"),
		"fresh-utc":        now.UTC().Format(time.RFC3339Nano),
		// A fresh stamp from a node whose zone is far from UTC: read as a local
		// time it would look hours old or hours in the future.
		"fresh-offset": now.In(time.FixedZone("x", -8*3600)).Format(time.RFC3339Nano),
		"garbage":      "not a time",
		"null":         nil,
	}
	for id, at := range rows {
		if _, err := db.Exec(`INSERT INTO namespace_clusters VALUES (?, 'provisioning', ?)`, id, at); err != nil {
			t.Fatal(err)
		}
	}
	modifier := fmt.Sprintf("-%d seconds", int(staleProvisioningAfter.Seconds()))

	ids := func(where string, args ...any) map[string]bool {
		r, err := db.Query(`SELECT id FROM namespace_clusters WHERE `+where, args...)
		if err != nil {
			t.Fatal(err)
		}
		defer r.Close()
		out := map[string]bool{}
		for r.Next() {
			var id string
			_ = r.Scan(&id)
			out[id] = true
		}
		return out
	}

	stale := ids(staleProvisioningPredicate, modifier)
	for _, id := range []string{"old-rfc3339", "old-nanos-offset", "old-sqlite"} {
		if !stale[id] {
			t.Errorf("%s should be stale", id)
		}
	}
	for _, id := range []string{"fresh-utc", "fresh-offset", "garbage", "null"} {
		if stale[id] {
			t.Errorf("%s must not be stale", id)
		}
	}
	unreadable := ids(`status = 'provisioning' AND datetime(provisioned_at) IS NULL`)
	if !unreadable["garbage"] || !unreadable["null"] || len(unreadable) != 2 {
		t.Errorf("unreadable set = %v, want garbage and null only", unreadable)
	}
}

func TestInsertCluster_stamps_provisioned_at_from_the_registry_clock(t *testing.T) {
	db := &recoveryMockDB{}
	cm := &ClusterManager{db: db, logger: zap.NewNop()}
	c := newProvisioningClusterFrom(BlueprintTenant(), 1, "ns", "wallet")

	if err := cm.insertCluster(context.Background(), c); err != nil {
		t.Fatal(err)
	}
	call := db.execCalls[0]
	if !strings.Contains(call.Query, "CURRENT_TIMESTAMP") || len(call.Args) != 8 {
		t.Fatalf("provisioned_at must be CURRENT_TIMESTAMP and not a bound Go time; query %q, %d args", call.Query, len(call.Args))
	}
}

func TestRollbackProvisioning_records_the_failure_once_with_the_given_reason(t *testing.T) {
	sw := newStaleSweep(nil, nil, false)
	sw.cm.systemdSpawner = &SystemdSpawner{systemdMgr: systemd.NewManager(t.TempDir(), zap.NewNop()), logger: zap.NewNop()}
	c := &NamespaceCluster{ID: "c1", NamespaceName: "ns"}

	sw.cm.rollbackProvisioning(context.Background(), c, nil, nil, "dns exploded")

	var marks []mockExecCall
	for _, call := range sw.db.execCalls {
		if strings.Contains(call.Query, "UPDATE namespace_clusters SET status") {
			marks = append(marks, call)
		}
	}
	if len(marks) != 1 {
		t.Fatalf("failure recorded %d times, want exactly 1", len(marks))
	}
	if !containsArg(marks[0].Args, "dns exploded") {
		t.Fatalf("the recorded message must be the caller's reason, args = %v", marks[0].Args)
	}
}

func containsArg(args []interface{}, want string) bool {
	for _, a := range args {
		if s, ok := a.(string); ok && s == want {
			return true
		}
	}
	return false
}

// A node with no overlay address is not sent the signed stop over its public
// one: the stop fails, nothing is released, and no request leaves the mesh.
func TestFailStaleProvisioning_a_node_without_an_overlay_address_keeps_its_ports(t *testing.T) {
	sw := newStaleSweep(oneStaleCluster, []staleClusterNode{{NodeID: "n1", InternalIP: ""}}, false)

	err := sw.cm.failStaleProvisioning(context.Background())
	if err == nil || !strings.Contains(err.Error(), "no overlay address") {
		t.Fatalf("err = %v, want the missing overlay address named", err)
	}
	if got := sw.count("stop:"); got != 0 {
		t.Fatalf("%d stop requests were sent to a node with no overlay address", got)
	}
	if countExecs(sw.db.recoveryMockDB, "DELETE FROM namespace_port_allocations") != 0 {
		t.Fatal("ports were released without the node's services being stopped")
	}
}
