package migrations_test

// Migration 044 is the data half of the operator fix: /v1/operator/* now asks
// whether the caller's wallet is on a list, and this creates and seeds it.
//
// It also removes plaintext invite tokens. An invite token is a credential for
// the cluster secret, the swarm key, the API-key HMAC secret, the RQLite
// password and the TURN secret, all of which /v1/internal/join returns in
// cleartext — so one readable in the registry is a key to the whole network.

import (
	"database/sql"
	"io/fs"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/DeBrosOfficial/network/migrations"
	"github.com/DeBrosOfficial/network/pkg/rqlite"
	_ "github.com/mattn/go-sqlite3"
	"go.uber.org/zap"
)

// migrationsBefore filters the embedded set, so a test can reach the schema as
// it was, seed the rows the bug produced, and let the real runner apply the
// migration over them.
func migrationsBefore(t *testing.T, prefix string) fs.FS {
	t.Helper()

	entries, err := fs.ReadDir(migrations.FS, ".")
	if err != nil {
		t.Fatalf("read embedded migrations: %v", err)
	}

	out := fstest.MapFS{}
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".sql") || name >= prefix {
			continue
		}
		body, err := fs.ReadFile(migrations.FS, name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		out[name] = &fstest.MapFile{Data: body}
	}
	if len(out) == 0 {
		t.Fatalf("no migrations before %s — the filter is wrong", prefix)
	}
	return out
}

func clusterBefore044(t *testing.T) *sql.DB {
	t.Helper()
	db := openRoundtripDB(t)

	if err := rqlite.ApplyEmbeddedMigrations(t.Context(), db, migrationsBefore(t, "044"), zap.NewNop()); err != nil {
		t.Fatalf("apply migrations before 044: %v", err)
	}

	exec := func(stmt string, args ...any) {
		t.Helper()
		if _, err := db.Exec(stmt, args...); err != nil {
			t.Fatalf("seed %q: %v", snippet(stmt), err)
		}
	}

	// Two nodes provisioned by the same operator, one by another, one never
	// claimed. Capitalisation differs because nothing normalised it.
	exec(`INSERT INTO dns_nodes (id, ip_address, operator_wallet) VALUES
		('n1', '1.1.1.1', '0xOperatorOne'),
		('n2', '2.2.2.2', '0xoperatorone'),
		('n3', '3.3.3.3', '0xOperatorTwo'),
		('n4', '4.4.4.4', ''),
		('n5', '5.5.5.5', NULL)`)

	exec(`INSERT INTO invite_tokens (token, created_by, expires_at) VALUES
		('plaintext-unused', 'operator:0xa', '2099-01-01 00:00:00')`)
	exec(`INSERT INTO invite_tokens (token, created_by, expires_at, used_at) VALUES
		('plaintext-used', 'operator:0xa', '2099-01-01 00:00:00', '2026-01-01 00:00:00')`)

	if err := rqlite.ApplyEmbeddedMigrations(t.Context(), db, migrations.FS, zap.NewNop()); err != nil {
		t.Fatalf("apply 044: %v", err)
	}
	return db
}

func operatorWallets(t *testing.T, db *sql.DB) []string {
	t.Helper()
	rows, err := db.Query(`SELECT wallet FROM operators ORDER BY wallet`)
	if err != nil {
		t.Fatalf("read operators: %v", err)
	}
	defer rows.Close()

	var out []string
	for rows.Next() {
		var w string
		if err := rows.Scan(&w); err != nil {
			t.Fatalf("scan: %v", err)
		}
		out = append(out, w)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("read operators: %v", err)
	}
	return out
}

// --operator-wallet is written into each node's config and echoed to dns_nodes
// on every heartbeat. It is the only record of who provisioned a node, so it
// is the genesis seed of the list.
func TestMigration044_seedsOperatorsFromTheNodesTheyProvisioned(t *testing.T) {
	db := clusterBefore044(t)

	got := operatorWallets(t, db)
	want := []string{"0xoperatorone", "0xoperatortwo"}

	if len(got) != len(want) {
		t.Fatalf("operators = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("operators = %v, want %v — one wallet, one row, normalised", got, want)
		}
	}
}

func TestMigration044_recordsHowEachOperatorGotThere(t *testing.T) {
	db := clusterBefore044(t)

	var addedBy string
	if err := db.QueryRow(`SELECT added_by FROM operators WHERE wallet = '0xoperatorone'`).Scan(&addedBy); err != nil {
		t.Fatalf("read added_by: %v", err)
	}
	if addedBy == "" {
		t.Error("the list cannot be audited: nothing records who let this wallet in")
	}
}

// A second apply must not wipe an operator added since the first.
func TestMigration044_isIdempotentForOperators(t *testing.T) {
	db := clusterBefore044(t)

	if _, err := db.Exec(
		`INSERT INTO operators (wallet, added_by) VALUES ('0xaddedlater', '0xoperatorone')`); err != nil {
		t.Fatalf("add an operator: %v", err)
	}
	if _, err := db.Exec(`DELETE FROM schema_migrations`); err != nil {
		t.Fatalf("clear schema_migrations: %v", err)
	}
	if err := rqlite.ApplyEmbeddedMigrations(t.Context(), db, migrations.FS, zap.NewNop()); err != nil {
		t.Fatalf("re-apply: %v", err)
	}

	for _, wallet := range operatorWallets(t, db) {
		if wallet == "0xaddedlater" {
			return
		}
	}
	t.Error("re-applying the migration removed an operator added after it first ran")
}

func TestMigration044_deletesPlaintextInviteTokens(t *testing.T) {
	db := clusterBefore044(t)

	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM invite_tokens`).Scan(&count); err != nil {
		t.Fatalf("count tokens: %v", err)
	}
	if count != 0 {
		t.Errorf("%d plaintext invite tokens survived; each is a key to every secret "+
			"the cluster holds", count)
	}
}

// The predicate is what makes the delete re-runnable. Without it a second
// apply would destroy every invite minted since the first.
func TestMigration044_leavesHashedTokensAlone(t *testing.T) {
	db := clusterBefore044(t)

	if _, err := db.Exec(`INSERT INTO invite_tokens (token, created_by, expires_at) VALUES
		('sha256:abc123', 'operator:0xa', '2099-01-01 00:00:00')`); err != nil {
		t.Fatalf("mint a token: %v", err)
	}
	if _, err := db.Exec(`DELETE FROM schema_migrations`); err != nil {
		t.Fatalf("clear schema_migrations: %v", err)
	}
	if err := rqlite.ApplyEmbeddedMigrations(t.Context(), db, migrations.FS, zap.NewNop()); err != nil {
		t.Fatalf("re-apply: %v", err)
	}

	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM invite_tokens WHERE token = 'sha256:abc123'`).Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 1 {
		t.Error("re-applying the migration deleted a token minted after it first ran")
	}
}
