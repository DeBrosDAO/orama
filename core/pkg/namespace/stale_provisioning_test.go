package namespace

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"

	"go.uber.org/zap"
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

func staleSweepManager(rows []NamespaceCluster, lostRace bool) (*ClusterManager, *guardedDB) {
	mock := &recoveryMockDB{queryFunc: func(dest any, query string, _ ...any) error {
		if d, ok := dest.(*[]NamespaceCluster); ok && strings.Contains(query, "status = 'provisioning'") {
			*d = rows
		}
		return nil
	}}
	db := &guardedDB{recoveryMockDB: mock, lostRace: lostRace}
	lg := zap.NewNop()
	cm := &ClusterManager{
		db:            db,
		logger:        lg,
		portAllocator: NewNamespacePortAllocator(db, lg),
		dnsManager:    NewDNSRecordManager(db, "example.test", lg),
		provisioning:  map[string]bool{},
	}
	return cm, db
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

func TestFailStaleProvisioning_fails_a_stale_cluster_once_and_rolls_back(t *testing.T) {
	old := time.Now().Add(-(provisioningTimeout + staleProvisioningMargin + time.Minute))
	cm, db := staleSweepManager([]NamespaceCluster{{ID: "c1", NamespaceName: "stale", ProvisionedAt: old}}, false)

	if err := cm.failStaleProvisioning(context.Background()); err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if got := countExecs(db.recoveryMockDB, "SET status = 'failed'"); got != 1 {
		t.Fatalf("failed updates = %d, want 1", got)
	}
	if countExecs(db.recoveryMockDB, "DELETE FROM namespace_port_allocations") != 1 {
		t.Fatal("port allocations were not released")
	}
	if countExecs(db.recoveryMockDB, "DELETE FROM dns_records") == 0 {
		t.Fatal("DNS records were not withdrawn")
	}
}

func TestFailStaleProvisioning_leaves_fresh_and_in_flight_clusters_alone(t *testing.T) {
	old := time.Now().Add(-(provisioningTimeout + staleProvisioningMargin + time.Minute))
	rows := []NamespaceCluster{
		{ID: "fresh", NamespaceName: "fresh", ProvisionedAt: time.Now().Add(-provisioningTimeout)},
		{ID: "inflight", NamespaceName: "inflight", ProvisionedAt: old},
	}
	cm, db := staleSweepManager(rows, false)
	cm.provisioning["inflight"] = true

	if err := cm.failStaleProvisioning(context.Background()); err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if len(db.execCalls) != 0 {
		t.Fatalf("no cluster should be touched, got %d writes", len(db.execCalls))
	}
}

func TestFailStaleProvisioning_losing_the_race_does_no_cleanup(t *testing.T) {
	old := time.Now().Add(-time.Hour)
	cm, db := staleSweepManager([]NamespaceCluster{{ID: "c1", NamespaceName: "stale", ProvisionedAt: old}}, true)

	if err := cm.failStaleProvisioning(context.Background()); err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if len(db.execCalls) != 1 {
		t.Fatalf("only the guarded UPDATE may run when another node won, got %d writes", len(db.execCalls))
	}
}

func TestFailStaleProvisioning_no_provisioning_clusters(t *testing.T) {
	cm, db := staleSweepManager(nil, false)
	if err := cm.failStaleProvisioning(context.Background()); err != nil || len(db.execCalls) != 0 {
		t.Fatalf("err = %v, writes = %d; want a no-op", err, len(db.execCalls))
	}
}
