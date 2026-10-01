package namespace

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/DeBrosOfficial/network/migrations"
	"github.com/DeBrosOfficial/network/pkg/rqlite"
	"github.com/DeBrosOfficial/network/pkg/rqlite/rqlitetest"
	"go.uber.org/zap"
)

// registryRig is the real registry schema — every migration, including the one
// that adds cluster_id and purge_data to namespace_pending_cleanup — in
// SQLite, with a ClusterManager reading and writing it. Remote spawn requests
// are recorded instead of sent.
type registryRig struct {
	t      *testing.T
	db     *sql.DB
	client rqlite.Client
	cm     *ClusterManager

	mu       sync.Mutex
	requests []map[string]interface{}
	sendErr  error
	nextID   int
}

func newRegistryRig(t *testing.T) *registryRig {
	t.Helper()
	c, db := rqlitetest.SQLite(t)
	if err := rqlite.ApplyEmbeddedMigrations(context.Background(), db, migrations.FS, zap.NewNop()); err != nil {
		t.Fatalf("apply migrations: %v", err)
	}
	r := &registryRig{t: t, db: db, client: c}
	r.cm = &ClusterManager{
		db:                  c,
		logger:              zap.NewNop(),
		localNodeID:         "coordinator",
		provisioning:        map[string]bool{},
		webrtcPortAllocator: NewWebRTCPortAllocator(c, zap.NewNop()),
		spawnRequestFn: func(_ context.Context, _ string, req map[string]interface{}) (*spawnResponse, error) {
			r.mu.Lock()
			defer r.mu.Unlock()
			r.requests = append(r.requests, req)
			if r.sendErr != nil {
				return nil, r.sendErr
			}
			return &spawnResponse{Success: true}, nil
		},
	}
	return r
}

func (r *registryRig) exec(query string, args ...any) {
	r.t.Helper()
	if _, err := r.db.Exec(query, args...); err != nil {
		r.t.Fatalf("%s: %v", query, err)
	}
}

func (r *registryRig) id() int { r.nextID++; return r.nextID }

func (r *registryRig) cluster(id, namespace string) {
	r.exec(`INSERT INTO namespace_clusters (id, namespace_id, namespace_name, status, provisioned_by) VALUES (?, ?, ?, 'ready', 'test')`,
		id, r.id(), namespace)
}

func (r *registryRig) allocation(cluster, node string) {
	n := r.id()
	r.exec(`INSERT INTO namespace_port_allocations (id, node_id, namespace_cluster_id, port_start, port_end, rqlite_http_port, rqlite_raft_port, olric_http_port, olric_memberlist_port, gateway_http_port)
		VALUES (?, ?, ?, 1, 5, 1, 2, 3, 4, 5)`, fmt.Sprint("p", n), node, cluster)
}

func (r *registryRig) membership(cluster, node string) {
	r.exec(`INSERT INTO namespace_cluster_nodes (id, namespace_cluster_id, node_id, role, status) VALUES (?, ?, ?, 'gateway', 'running')`,
		fmt.Sprint("m", r.id()), cluster, node)
}

func (r *registryRig) webrtc(cluster, node, serviceType string) {
	r.exec(`INSERT INTO webrtc_port_allocations (id, node_id, namespace_cluster_id, service_type) VALUES (?, ?, ?, ?)`,
		fmt.Sprint("w", r.id()), node, cluster, serviceType)
}

// pending writes a row as recordPendingCleanup does.
func (r *registryRig) pending(namespace, node, action, cluster string, purge bool) {
	r.cm.recordPendingCleanup(context.Background(), namespace, node, "10.0.0."+node[len(node)-1:], action,
		cleanupScope{ClusterID: cluster, PurgeData: purge}, errors.New("node unreachable"))
}

func (r *registryRig) pendingCount() int {
	var n int
	if err := r.db.QueryRow(`SELECT COUNT(*) FROM namespace_pending_cleanup`).Scan(&n); err != nil {
		r.t.Fatal(err)
	}
	return n
}

func (r *registryRig) sent() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []string
	for _, req := range r.requests {
		out = append(out, fmt.Sprintf("%v:%v@%v", req["action"], req["namespace"], req["node_id"]))
	}
	return out
}

func (r *registryRig) replay() error {
	r.mu.Lock()
	r.requests = nil
	r.mu.Unlock()
	return r.cm.replayPendingCleanups(context.Background())
}

// T1, the bug: acme is deleted and its teardown fails on n1, so it is owed;
// acme is created again, under a new cluster id, and placed on n1 before the
// replay. The replay tore down the NEW namespace and deleted its data.
func TestReplayPendingCleanups_doesNotTearDownANamespaceCreatedAgainOnThatNode(t *testing.T) {
	r := newRegistryRig(t)
	r.pending("acme", "node1", teardownAction, "c-old", true)
	r.cluster("c-new", "acme")
	r.allocation("c-new", "node1")

	if err := r.replay(); err != nil {
		t.Fatal(err)
	}
	if sent := r.sent(); len(sent) != 0 {
		t.Fatalf("sent %v: the replay would tear down the namespace that was created again", sent)
	}
	if r.pendingCount() != 0 {
		t.Fatal("the superseded cleanup was kept: it would be reconsidered every sweep for ever")
	}
}

func TestReplayPendingCleanups_aNamespaceGoneFromTheRegistryIsStillTornDown(t *testing.T) {
	r := newRegistryRig(t)
	r.pending("acme", "node1", teardownAction, "c-old", true)

	if err := r.replay(); err != nil {
		t.Fatal(err)
	}
	if got := r.sent(); len(got) != 1 || got[0] != "teardown-namespace:acme@node1" {
		t.Fatalf("sent %v, want the teardown owed", got)
	}
	if purge, _ := r.requests[0]["purge_data"].(bool); !purge {
		t.Fatalf("the delete's purge_data was lost on replay: %v", r.requests[0])
	}
	if r.pendingCount() != 0 {
		t.Fatal("a teardown that succeeded stayed in the retry queue")
	}
}

// A rollback's teardown is owed while the same cluster's allocation rows are
// still there: the incarnation is the same, so the replay goes ahead.
func TestReplayPendingCleanups_theSameClusterStillRegisteredIsStillTornDown(t *testing.T) {
	r := newRegistryRig(t)
	r.cluster("c1", "acme")
	r.allocation("c1", "node1")
	r.pending("acme", "node1", teardownAction, "c1", false)

	if err := r.replay(); err != nil {
		t.Fatal(err)
	}
	if got := r.sent(); len(got) != 1 {
		t.Fatalf("sent %v, want the teardown of the cluster being rolled back", got)
	}
	if _, has := r.requests[0]["purge_data"]; has {
		t.Fatalf("a rollback asked for the tenant data to be purged: %v", r.requests[0])
	}
}

// Rows written before the cluster_id column have none: any assignment of the
// name to that node is treated as another incarnation.
func TestReplayPendingCleanups_aRowWithoutAClusterIDYieldsToAnyAssignment(t *testing.T) {
	r := newRegistryRig(t)
	r.cluster("c-new", "acme")
	r.membership("c-new", "node1")
	r.pending("acme", "node1", teardownAction, "", false)

	_ = r.replay()
	if len(r.sent()) != 0 {
		t.Fatalf("sent %v", r.sent())
	}
}

func TestReplayPendingCleanups_anotherNodesAssignmentDoesNotSupersede(t *testing.T) {
	r := newRegistryRig(t)
	r.cluster("c-new", "acme")
	r.allocation("c-new", "node2")
	r.pending("acme", "node1", teardownAction, "c-old", false)

	_ = r.replay()
	if got := r.sent(); len(got) != 1 {
		t.Fatalf("sent %v, want the teardown of node1 (acme is on node2 only)", got)
	}
}

func TestReplayPendingCleanups_anotherNamespacesAssignmentDoesNotSupersede(t *testing.T) {
	r := newRegistryRig(t)
	r.cluster("c-other", "acme-corp")
	r.allocation("c-other", "node1")
	r.pending("acme", "node1", teardownAction, "c-old", false)

	_ = r.replay()
	if len(r.sent()) != 1 {
		t.Fatalf("sent %v, want the teardown of acme", r.sent())
	}
}

// The same check covers the WebRTC teardowns.
func TestReplayPendingCleanups_aWebRTCTeardownYieldsToAnotherIncarnation(t *testing.T) {
	for _, action := range []string{teardownSFUAction, teardownTURNAction} {
		t.Run(action, func(t *testing.T) {
			r := newRegistryRig(t)
			r.cluster("c-new", "acme")
			r.allocation("c-new", "node1")
			r.pending("acme", "node1", action, "c-old", false)

			_ = r.replay()
			if len(r.sent()) != 0 || r.pendingCount() != 0 {
				t.Fatalf("sent %v, pending %d", r.sent(), r.pendingCount())
			}
		})
	}
}

// WebRTC turned off on a namespace that stays: the cluster is the same, its SFU
// allocation is still there until the disable finishes, and the SFU must go.
func TestReplayPendingCleanups_aWebRTCTeardownOfTheSameClusterIsStillSent(t *testing.T) {
	r := newRegistryRig(t)
	r.cluster("c1", "acme")
	r.allocation("c1", "node1")
	r.webrtc("c1", "node1", "sfu")
	r.pending("acme", "node1", teardownSFUAction, "c1", false)

	_ = r.replay()
	if got := r.sent(); len(got) != 1 || got[0] != "teardown-sfu:acme@node1" {
		t.Fatalf("sent %v", got)
	}
}

// Another incarnation with its own SFU on the node: not this one's to remove,
// even when the membership of the name has not reached that node.
func TestReplayPendingCleanups_anSFUOfAnotherIncarnationIsNotTornDown(t *testing.T) {
	r := newRegistryRig(t)
	r.cluster("c-new", "acme")
	r.webrtc("c-new", "node1", "sfu")
	r.pending("acme", "node1", teardownSFUAction, "c-old", false)

	_ = r.replay()
	if len(r.sent()) != 0 {
		t.Fatalf("sent %v", r.sent())
	}
}

// When the registry cannot answer, a destructive cleanup is not sent.
func TestReplayPendingCleanups_doesNotSendATeardownItCannotCheck(t *testing.T) {
	r := newRegistryRig(t)
	r.pending("acme", "node1", teardownAction, "c-old", true)
	r.exec(`DROP TABLE namespace_port_allocations`)

	err := r.replay()
	if err == nil || !strings.Contains(err.Error(), "acme") {
		t.Fatalf("err = %v, want the failed check reported", err)
	}
	if len(r.sent()) != 0 {
		t.Fatalf("sent %v without being able to check the registry", r.sent())
	}
	if r.pendingCount() != 1 {
		t.Fatal("an unchecked cleanup was dropped")
	}
}

// A stop is not destructive: it is replayed without consulting the registry.
func TestReplayPendingCleanups_aStopIsReplayedAsIs(t *testing.T) {
	r := newRegistryRig(t)
	r.cluster("c-new", "acme")
	r.allocation("c-new", "node1")
	r.pending("acme", "node1", "stop-rqlite", "", false)

	_ = r.replay()
	if got := r.sent(); len(got) != 1 || got[0] != "stop-rqlite:acme@node1" {
		t.Fatalf("sent %v", got)
	}
}

func TestRecordPendingCleanup_keepsTheClusterAndUpgradesToPurge(t *testing.T) {
	r := newRegistryRig(t)
	r.pending("acme", "node1", teardownAction, "c1", false)
	r.pending("acme", "node1", teardownAction, "c1", true)
	r.pending("acme", "node1", teardownAction, "", false) // a retry that does not know the cluster

	var cluster string
	var purge, attempts int
	if err := r.db.QueryRow(`SELECT cluster_id, purge_data, attempts FROM namespace_pending_cleanup`).Scan(&cluster, &purge, &attempts); err != nil {
		t.Fatal(err)
	}
	if cluster != "c1" || purge != 1 || attempts != 3 {
		t.Fatalf("cluster = %q, purge = %d, attempts = %d", cluster, purge, attempts)
	}
}

// Allocating the namespace a port block on the node withdraws the teardowns
// still owed there for an incarnation that is gone.
func TestAllocatePortBlock_withdrawsThePendingTeardownsOfThatNamespaceOnThatNode(t *testing.T) {
	r := newRegistryRig(t)
	r.cluster("c-new", "acme")
	r.pending("acme", "node1", teardownAction, "c-old", true)
	r.pending("acme", "node2", teardownAction, "c-old", true) // another node
	r.pending("other", "node1", teardownAction, "c-x", false) // another namespace
	r.pending("acme", "node1", "stop-rqlite", "", false)      // not destructive, not ours to withdraw

	npa := NewNamespacePortAllocator(r.client, zap.NewNop())
	if _, err := npa.AllocatePortBlock(context.Background(), "node1", "c-new", BlueprintTenant()); err != nil {
		t.Fatal(err)
	}

	rows, err := r.db.Query(`SELECT namespace, node_id, action FROM namespace_pending_cleanup ORDER BY namespace, node_id, action`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var left []string
	for rows.Next() {
		var ns, node, action string
		if err := rows.Scan(&ns, &node, &action); err != nil {
			t.Fatal(err)
		}
		left = append(left, ns+"/"+node+"/"+action)
	}
	want := "acme/node1/stop-rqlite acme/node2/teardown-namespace other/node1/teardown-namespace"
	if got := strings.Join(left, " "); got != want {
		t.Fatalf("left %q, want %q", got, want)
	}
}

func TestAllocateSFUPorts_withdrawsThePendingSFUTeardown(t *testing.T) {
	r := newRegistryRig(t)
	r.cluster("c-new", "acme")
	r.exec(`INSERT INTO dns_nodes (id, ip_address, internal_ip, status) VALUES ('node1', '192.0.2.1', '10.0.0.1', 'active')`)
	r.pending("acme", "node1", teardownSFUAction, "c-old", false)
	r.pending("acme", "node1", teardownTURNAction, "c-old", false)

	wpa := NewWebRTCPortAllocator(r.client, zap.NewNop())
	if _, err := wpa.AllocateSFUPorts(context.Background(), "node1", "c-new"); err != nil {
		t.Fatal(err)
	}
	var action string
	if err := r.db.QueryRow(`SELECT action FROM namespace_pending_cleanup`).Scan(&action); err != nil || action != teardownTURNAction {
		t.Fatalf("remaining = %q (%v), want only the TURN teardown: the SFU one would remove the new SFU", action, err)
	}
}

// Only the delete purges tenant data, and what it asked for travels to the node.
func TestDeprovisionCluster_asksEveryNodeToPurgeTheTenantData(t *testing.T) {
	r := newRegistryRig(t)
	lg := zap.NewNop()
	r.cm.portAllocator = NewNamespacePortAllocator(r.client, lg)
	r.cm.webrtcPortAllocator = NewWebRTCPortAllocator(r.client, lg)
	r.cm.dnsManager = NewDNSRecordManager(r.client, "example.test", lg)
	r.exec(`INSERT INTO dns_nodes (id, ip_address, internal_ip, status) VALUES ('node1', '192.0.2.1', '10.0.0.1', 'active')`)
	r.cluster("c1", "acme")
	r.membership("c1", "node1")
	var nsID int64
	if err := r.db.QueryRow(`SELECT namespace_id FROM namespace_clusters WHERE id = 'c1'`).Scan(&nsID); err != nil {
		t.Fatal(err)
	}

	if err := r.cm.DeprovisionCluster(context.Background(), nsID); err != nil {
		t.Fatal(err)
	}
	var teardown map[string]interface{}
	for _, req := range r.requests {
		if req["action"] == teardownAction {
			teardown = req
		}
	}
	if teardown == nil {
		t.Fatalf("no teardown was sent: %v", r.requests)
	}
	if purge, _ := teardown["purge_data"].(bool); !purge {
		t.Fatalf("the delete did not ask for the tenant data to be purged: %v", teardown)
	}
}
