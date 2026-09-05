package auth

import (
	"context"
	"database/sql"
	"testing"

	"github.com/DeBrosOfficial/network/migrations"
	"github.com/DeBrosOfficial/network/pkg/rqlite"
	_ "github.com/mattn/go-sqlite3"
	"go.uber.org/zap"
)

// The retention window is a date comparison against SQLite's own clock, and a
// fake that answers "DELETE" with success would pass whatever the predicate
// said — including one that deletes everything. These run the real statement
// against the real schema.

func realAuditLog(t *testing.T) (*AuditLog, *sql.DB) {
	t.Helper()

	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	if err := rqlite.ApplyEmbeddedMigrations(t.Context(), db, migrations.FS, zap.NewNop()); err != nil {
		t.Fatalf("apply migrations: %v", err)
	}
	return NewAuditLog(registryOf(&sqliteNet{db: &sqliteDatabase{db: db}}), nil), db
}

// insertAuditEvent writes one row aged by the given SQLite modifier, e.g.
// "-91 days".
func insertAuditEvent(t *testing.T, db *sql.DB, action, age string) {
	t.Helper()
	_, err := db.Exec(
		`INSERT INTO audit_events(namespace, actor, action, result, created_at)
		 VALUES ('acme', '0xowner', ?, 'success', datetime('now', ?))`, action, age)
	if err != nil {
		t.Fatalf("insert %s: %v", action, err)
	}
}

func auditActionsInTable(t *testing.T, db *sql.DB) []string {
	t.Helper()
	rows, err := db.Query(`SELECT action FROM audit_events ORDER BY id`)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	defer rows.Close()

	var out []string
	for rows.Next() {
		var action string
		if err := rows.Scan(&action); err != nil {
			t.Fatal(err)
		}
		out = append(out, action)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestAuditLog_pruneRemovesOnlyEventsPastRetention(t *testing.T) {
	log, db := realAuditLog(t)

	insertAuditEvent(t, db, AuditKeyIssued, "-91 days")
	insertAuditEvent(t, db, AuditKeyRevoked, "-89 days")
	insertAuditEvent(t, db, AuditVerifySucceeded, "-1 minutes")

	if err := log.Prune(context.Background()); err != nil {
		t.Fatalf("Prune: %v", err)
	}

	got := auditActionsInTable(t, db)
	if len(got) != 2 {
		t.Fatalf("%d events remain (%v), want the two inside the window", len(got), got)
	}
	if got[0] != AuditKeyRevoked || got[1] != AuditVerifySucceeded {
		t.Errorf("remaining = %v, want the 89-day-old and the recent one", got)
	}
}

// Pruning an empty window must leave the trail alone; a predicate that read the
// retention as "everything older than now" would empty the table on the first
// tick and nobody would notice until they needed it.
func TestAuditLog_pruneKeepsEverythingInsideTheWindow(t *testing.T) {
	log, db := realAuditLog(t)

	insertAuditEvent(t, db, AuditKeyIssued, "-1 days")
	insertAuditEvent(t, db, AuditGrantAdded, "-30 days")

	if err := log.Prune(context.Background()); err != nil {
		t.Fatalf("Prune: %v", err)
	}
	if got := auditActionsInTable(t, db); len(got) != 2 {
		t.Errorf("%d events remain (%v), want 2", len(got), got)
	}
}

// The events Record writes have to be the events Prune can see: a column name
// or a table name that drifted would leave the trail growing for ever.
func TestAuditLog_recordThenPruneRoundTrips(t *testing.T) {
	log, db := realAuditLog(t)

	log.Record(context.Background(), AuditEvent{
		Namespace: "acme",
		Actor:     "0xowner",
		Action:    AuditNamespaceDeleted,
		Resource:  "acme",
		Result:    AuditSuccess,
	})

	if got := auditActionsInTable(t, db); len(got) != 1 || got[0] != AuditNamespaceDeleted {
		t.Fatalf("after Record the table holds %v", got)
	}
	if err := log.Prune(context.Background()); err != nil {
		t.Fatalf("Prune: %v", err)
	}
	if got := auditActionsInTable(t, db); len(got) != 1 {
		t.Errorf("a brand new event was pruned: %v", got)
	}
}

func TestAuditLog_pruneWithNoDatabaseIsANoOp(t *testing.T) {
	var nilLog *AuditLog
	if err := nilLog.Prune(context.Background()); err != nil {
		t.Errorf("Prune on a nil log: %v", err)
	}
	if err := NewAuditLog(nil, nil).Prune(context.Background()); err != nil {
		t.Errorf("Prune with no client: %v", err)
	}
}
