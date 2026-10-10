package migrations_test

// Migration 063 decides who may create a namespace.
//
// A registry that already has data keeps today's behaviour: any signed-in
// wallet. A registry whose only namespace is the default row migration 001
// seeds, with no operator and no node, is a new cluster. No row is written,
// and the gateway reads a missing row as operators.

import (
	"database/sql"
	"testing"

	"github.com/DeBrosOfficial/network/migrations"
	"github.com/DeBrosOfficial/network/pkg/gateway/handlers/operator"
	"github.com/DeBrosOfficial/network/pkg/rqlite"
	_ "github.com/mattn/go-sqlite3"
	"go.uber.org/zap"
)

func applyBefore063(t *testing.T) *sql.DB {
	t.Helper()
	db := openRoundtripDB(t)
	if err := rqlite.ApplyEmbeddedMigrations(t.Context(), db, migrationsBefore(t, "063"), zap.NewNop()); err != nil {
		t.Fatalf("apply migrations before 063: %v", err)
	}
	return db
}

func apply063(t *testing.T, db *sql.DB) {
	t.Helper()
	if err := rqlite.ApplyEmbeddedMigrations(t.Context(), db, migrations.FS, zap.NewNop()); err != nil {
		t.Fatalf("apply 063: %v", err)
	}
}

func creationValue(t *testing.T, db *sql.DB) (string, bool) {
	t.Helper()
	var value string
	err := db.QueryRow(`SELECT value FROM cluster_settings WHERE key = ?`, operator.SettingNamespaceCreation).Scan(&value)
	if err == sql.ErrNoRows {
		return "", false
	}
	if err != nil {
		t.Fatalf("read namespace_creation: %v", err)
	}
	return value, true
}

func TestMigration063_existingClusterBecomesOpen(t *testing.T) {
	cases := []struct {
		name string
		seed string
	}{
		{"a namespace besides default", `INSERT INTO namespaces (name) VALUES ('anchat')`},
		{"a node", `INSERT INTO dns_nodes (id, ip_address) VALUES ('n1', '1.1.1.1')`},
		{"an operator", `INSERT INTO operators (wallet, added_by) VALUES ('0xoperator', 'test')`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			db := applyBefore063(t)
			if _, err := db.Exec(tc.seed); err != nil {
				t.Fatalf("seed: %v", err)
			}
			apply063(t, db)
			got, ok := creationValue(t, db)
			if !ok || got != operator.CreationOpen {
				t.Fatalf("namespace_creation = %q (present %v), want open", got, ok)
			}
		})
	}
}

func TestMigration063_newClusterResolvesToOperators(t *testing.T) {
	db := openRoundtripDB(t)
	if err := rqlite.ApplyEmbeddedMigrations(t.Context(), db, migrations.FS, zap.NewNop()); err != nil {
		t.Fatalf("apply: %v", err)
	}

	// The seeded default namespace is not "a cluster that already has data".
	if _, ok := creationValue(t, db); ok {
		t.Fatal("a new registry stored namespace_creation; no row is what resolves to operators")
	}
	var caps int
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM cluster_settings WHERE key = ?`, operator.SettingMaxNamespacesPerWallet,
	).Scan(&caps); err != nil {
		t.Fatal(err)
	}
	if caps != 0 {
		t.Fatalf("a new registry stored a wallet cap (%d rows); the default is the code's", caps)
	}

	policy, err := operator.LoadCreationPolicy(t.Context(), rqlite.NewClient(db))
	if err != nil {
		t.Fatal(err)
	}
	if policy.Mode != operator.CreationOperators {
		t.Fatalf("mode %q, want operators", policy.Mode)
	}
	if policy.WalletCap != operator.DefaultMaxNamespacesPerWallet {
		t.Fatalf("cap %d, want %d", policy.WalletCap, operator.DefaultMaxNamespacesPerWallet)
	}
}

// A retried apply must not put open back over a choice the operator made after
// the upgrade.
func TestMigration063_doesNotOverwriteALaterChoice(t *testing.T) {
	db := applyBefore063(t)
	if _, err := db.Exec(`INSERT INTO namespaces (name) VALUES ('anchat')`); err != nil {
		t.Fatal(err)
	}
	apply063(t, db)
	if _, err := db.Exec(
		`UPDATE cluster_settings SET value = ? WHERE key = ?`,
		operator.CreationOperators, operator.SettingNamespaceCreation); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`DELETE FROM schema_migrations WHERE version = 63`); err != nil {
		t.Fatal(err)
	}
	apply063(t, db)
	got, ok := creationValue(t, db)
	if !ok || got != operator.CreationOperators {
		t.Fatalf("namespace_creation = %q (present %v), want operators — the retry reopened creation", got, ok)
	}
}
