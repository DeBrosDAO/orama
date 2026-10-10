package node

import (
	"context"
	"database/sql"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/logging"
)

// A replica's A record was written with the node's WireGuard address, and the
// heartbeat cleanup removed private addresses only from system records.
func TestCleanupPrivateIPRecords_removesDeploymentRecordsToo(t *testing.T) {
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	mustExec(t, db, `CREATE TABLE dns_records (fqdn TEXT, record_type TEXT, value TEXT,
		namespace TEXT, deployment_id TEXT, UNIQUE(fqdn, record_type, value))`)
	for _, r := range [][]any{
		{"web-abc.example.test.", "A", "10.0.0.2", "acme", "d1"},     // replica's overlay address: removed
		{"web-abc.example.test.", "A", "203.0.113.9", "acme", "d1"},  // public: kept
		{"node-1.example.test.", "A", "10.0.0.1", "system", nil},     // system private: removed
		{"ns-acme.example.test.", "A", "10.0.0.3", "namespace", nil}, // neither: left alone
		{"web-abc.example.test.", "TXT", "10.0.0.2", "acme", "d1"},   // not an A record: kept
	} {
		mustExec(t, db, `INSERT INTO dns_records VALUES (?, ?, ?, ?, ?)`, r...)
	}
	lg, err := logging.NewColoredLogger(logging.ComponentNode, false)
	if err != nil {
		t.Fatal(err)
	}

	cleanupPrivateIPRecords(context.Background(), db, lg)

	if n := countRecords(t, db, `value = '10.0.0.2' AND record_type = 'A'`); n != 0 {
		t.Error("the replica's overlay A record survived")
	}
	if n := countRecords(t, db, `namespace = 'system'`); n != 0 {
		t.Error("the system private record survived")
	}
	if n := countRecords(t, db, `value = '203.0.113.9' OR record_type = 'TXT' OR namespace = 'namespace'`); n != 3 {
		t.Errorf("%d of the 3 records that must stay remain", n)
	}
}
