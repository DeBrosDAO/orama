package rqlite_test

import (
	"bytes"
	"context"
	"database/sql"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync"
	"testing"
	"testing/fstest"

	"github.com/DeBrosOfficial/network/migrations"
	"github.com/DeBrosOfficial/network/pkg/rqlite"
	"github.com/DeBrosOfficial/network/pkg/rqlite/rqlitetest"
	_ "github.com/rqlite/gorqlite/stdlib"
)

// A migration is one transaction. These tests run against a real rqlited, with
// a proxy in front of it that loses one /db/execute request the way an
// overloaded node loses its raft leader ("503 leader not found"). Against the
// old engine, which sent each statement and then the tracker row as separate
// requests, losing the second request left the migration half-applied and the
// retry failed "no such table".

const leaderLostBody = "leader not found"

// faultyNode is a real rqlited behind a proxy that answers 503 to the first
// /db/execute request whose body contains marker.
type faultyNode struct {
	db *sql.DB

	mu     sync.Mutex
	marker string
	fired  bool
}

func startFaultyNode(t *testing.T) *faultyNode {
	t.Helper()
	upstream, err := url.Parse("http://" + rqlitetest.StartNode(t))
	if err != nil {
		t.Fatalf("parse upstream: %v", err)
	}
	n := &faultyNode{}
	proxy := httputil.NewSingleHostReverseProxy(upstream)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, "/db/execute") {
			body, _ := io.ReadAll(r.Body)
			r.Body = io.NopCloser(bytes.NewReader(body))
			if n.shouldDrop(string(body)) {
				http.Error(w, leaderLostBody, http.StatusServiceUnavailable)
				return
			}
		}
		proxy.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)

	db, err := sql.Open("rqlite", srv.URL+"?disableClusterDiscovery=true&level=weak")
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	n.db = db
	return n
}

// loseNextExecuteContaining makes the next matching request fail once.
func (n *faultyNode) loseNextExecuteContaining(marker string) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.marker, n.fired = marker, false
}

func (n *faultyNode) shouldDrop(body string) bool {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.marker == "" || n.fired || !strings.Contains(body, n.marker) {
		return false
	}
	n.fired = true
	return true
}

func (n *faultyNode) dropped() bool {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.fired
}

func (n *faultyNode) tableExists(t *testing.T, name string) bool {
	t.Helper()
	var c int
	if err := n.db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?`, name).Scan(&c); err != nil {
		t.Fatalf("check table %s: %v", name, err)
	}
	return c > 0
}

func (n *faultyNode) recordedVersions(t *testing.T, tracker string) []int {
	t.Helper()
	rows, err := n.db.Query(`SELECT version FROM ` + tracker + ` ORDER BY version`)
	if err != nil {
		t.Fatalf("read %s: %v", tracker, err)
	}
	defer rows.Close()
	var out []int
	for rows.Next() {
		var v int
		if err := rows.Scan(&v); err != nil {
			t.Fatal(err)
		}
		out = append(out, v)
	}
	return out
}

func sameInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// renameFS: 001 makes t, 002 renames it and then indexes the new name. The
// rename is what makes a half-applied 002 unrecoverable by a blind re-run.
func renameFS() fstest.MapFS {
	return fstest.MapFS{
		"001_create.sql": {Data: []byte(`CREATE TABLE t (id INTEGER PRIMARY KEY, v TEXT); INSERT INTO t(v) VALUES ('a');`)},
		"002_rename.sql": {Data: []byte(`ALTER TABLE t RENAME TO t2; CREATE INDEX idx_t2_v ON t2(v);`)},
	}
}

type applyFn func(ctx context.Context, db *sql.DB, fsys fstest.MapFS) error

var engines = []struct {
	name    string
	tracker string
	apply   applyFn
}{
	{"main", "schema_migrations", func(ctx context.Context, db *sql.DB, f fstest.MapFS) error {
		return rqlite.ApplyEmbeddedMigrations(ctx, db, f, nil)
	}},
	{"namespace", rqlite.NamespaceMigrationsTracker(), func(ctx context.Context, db *sql.DB, f fstest.MapFS) error {
		return rqlite.ApplyEmbeddedMigrationsNamespace(ctx, db, f, nil)
	}},
}

func TestApplyMigrations_lostRequestLeavesNothingHalfApplied_liveRqlite(t *testing.T) {
	for _, eng := range engines {
		t.Run(eng.name, func(t *testing.T) {
			n := startFaultyNode(t)
			ctx := context.Background()

			n.loseNextExecuteContaining("idx_t2_v")
			if err := eng.apply(ctx, n.db, renameFS()); err == nil {
				t.Fatal("first apply succeeded; the injected 503 never reached the engine")
			}
			if !n.dropped() {
				t.Fatal("the fault proxy never saw the marked request")
			}

			// Atomic: migration 002 did not happen at all.
			if !n.tableExists(t, "t") || n.tableExists(t, "t2") {
				t.Fatalf("migration 002 is half-applied: t exists=%v, t2 exists=%v",
					n.tableExists(t, "t"), n.tableExists(t, "t2"))
			}
			if got := n.recordedVersions(t, eng.tracker); !sameInts(got, []int{1}) {
				t.Fatalf("recorded versions after the lost request = %v, want [1]", got)
			}

			// The retry converges: no "no such table", everything recorded.
			if err := eng.apply(ctx, n.db, renameFS()); err != nil {
				t.Fatalf("retry after the lost request failed: %v", err)
			}
			if n.tableExists(t, "t") || !n.tableExists(t, "t2") {
				t.Fatal("after the retry t2 should exist and t should not")
			}
			if got := n.recordedVersions(t, eng.tracker); !sameInts(got, []int{1, 2}) {
				t.Fatalf("recorded versions after the retry = %v, want [1 2]", got)
			}
		})
	}
}

func TestApplyMigrations_failingStatementRollsBackTheMigration_liveRqlite(t *testing.T) {
	fsys := renameFS()
	fsys["002_rename.sql"] = &fstest.MapFile{Data: []byte(
		`ALTER TABLE t RENAME TO t2; INSERT INTO nosuch_table VALUES (1);`)}

	for _, eng := range engines {
		t.Run(eng.name, func(t *testing.T) {
			n := startFaultyNode(t)
			err := eng.apply(context.Background(), n.db, fsys)
			if err == nil || !strings.Contains(err.Error(), "no such table: nosuch_table") {
				t.Fatalf("err = %v, want the failing statement's error", err)
			}
			if !n.tableExists(t, "t") || n.tableExists(t, "t2") {
				t.Fatal("the rename survived the failed statement after it; the migration is not atomic")
			}
			if got := n.recordedVersions(t, eng.tracker); !sameInts(got, []int{1}) {
				t.Fatalf("recorded versions = %v, want [1]", got)
			}
		})
	}
}

// A database a pre-atomic engine left half-migrated: the rename happened, the
// migration was never recorded. "already exists" must still be tolerated, and
// the tolerance must work inside a transaction, which aborts on its first error.
func TestApplyMigrations_toleratesObjectsALegacyEngineAlreadyCreated_liveRqlite(t *testing.T) {
	fsys := fstest.MapFS{
		"001_two_tables.sql": {Data: []byte(`CREATE TABLE a (id INTEGER); CREATE TABLE b (id INTEGER); CREATE TABLE c (id INTEGER);`)},
	}
	for _, eng := range engines {
		t.Run(eng.name, func(t *testing.T) {
			n := startFaultyNode(t)
			if _, err := n.db.Exec(`CREATE TABLE a (id INTEGER)`); err != nil {
				t.Fatal(err)
			}
			if _, err := n.db.Exec(`CREATE TABLE c (id INTEGER)`); err != nil {
				t.Fatal(err)
			}
			if err := eng.apply(context.Background(), n.db, fsys); err != nil {
				t.Fatalf("apply over a half-migrated database: %v", err)
			}
			if !n.tableExists(t, "b") {
				t.Fatal("the statement that was not yet applied (CREATE TABLE b) did not run")
			}
			if got := n.recordedVersions(t, eng.tracker); !sameInts(got, []int{1}) {
				t.Fatalf("recorded versions = %v, want [1]", got)
			}
		})
	}
}

// Every shipped migration must be legal inside a transaction, and the two
// engines must both get the real schema through rqlite in one piece.
func TestApplyMigrations_realSchemaAppliesAsTransactions_liveRqlite(t *testing.T) {
	for _, eng := range engines {
		t.Run(eng.name, func(t *testing.T) {
			n := startFaultyNode(t)
			ctx := context.Background()
			apply := func() error {
				if eng.name == "main" {
					return rqlite.ApplyEmbeddedMigrations(ctx, n.db, migrations.FS, nil)
				}
				return rqlite.ApplyEmbeddedMigrationsNamespace(ctx, n.db, migrations.FS, nil)
			}
			if err := apply(); err != nil {
				t.Fatalf("apply the embedded schema: %v", err)
			}
			first := n.recordedVersions(t, eng.tracker)
			if len(first) == 0 {
				t.Fatal("no migration was recorded")
			}
			if err := apply(); err != nil {
				t.Fatalf("second apply is not a no-op: %v", err)
			}
			if got := n.recordedVersions(t, eng.tracker); !sameInts(got, first) {
				t.Fatalf("second apply changed the tracker: %v -> %v", first, got)
			}
		})
	}
}
