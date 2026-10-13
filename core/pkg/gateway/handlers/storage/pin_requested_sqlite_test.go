package storage

import (
	"context"
	"database/sql"
	"os"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/rqlite"
	_ "github.com/mattn/go-sqlite3"
)

// sqliteDB is an rqlite.Client over a real SQLite for the two calls the
// ownership code makes. The schema is the one the migrations create.
type sqliteDB struct {
	rqlite.Client
	db *sql.DB
}

func (s *sqliteDB) Query(ctx context.Context, dest any, query string, args ...any) error {
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	cols, err := rows.Columns()
	if err != nil {
		return err
	}
	out := dest.(*[]map[string]interface{})
	for rows.Next() {
		vals := make([]interface{}, len(cols))
		ptrs := make([]interface{}, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return err
		}
		row := map[string]interface{}{}
		for i, c := range cols {
			row[c] = vals[i]
		}
		*out = append(*out, row)
	}
	return rows.Err()
}

func (s *sqliteDB) Exec(ctx context.Context, query string, args ...any) (sql.Result, error) {
	return s.db.ExecContext(ctx, query, args...)
}

func ownershipSchema(t *testing.T) *sqliteDB {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec(`CREATE TABLE schema_migrations (version INTEGER PRIMARY KEY)`); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"008_ipfs_namespace_tracking.sql", "058_ipfs_pin_requested_at.sql"} {
		ddl, err := os.ReadFile("../../../../migrations/" + f)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(string(ddl)); err != nil {
			t.Fatalf("apply %s: %v", f, err)
		}
	}
	return &sqliteDB{db: db}
}

// bugboard #414: a re-upload or pin of content the namespace already owns is
// a fresh pin request, even though the ownership row (and its first
// uploaded_at) is kept. Without this a download right after re-uploading
// identical bytes was answered as gone while the pin propagated.
func TestPinRequestedAt_movesOnReuploadAndPin(t *testing.T) {
	db := ownershipSchema(t)
	h := New(&mockIPFSClient{}, newTestLogger(), Config{}, db, nil)
	ctx := context.Background()
	const ns = "anchat"

	if err := h.recordCIDOwnership(ctx, testCID, ns, "memo.m4a", ns, 10); err != nil {
		t.Fatal(err)
	}
	// Age the row: first upload and last pin request an hour ago.
	if _, err := db.db.Exec(`UPDATE ipfs_content_ownership SET uploaded_at = datetime('now','-1 hour'), pin_requested_at = datetime('now','-1 hour')`); err != nil {
		t.Fatal(err)
	}
	recent := func() bool {
		ok, err := h.pinRequestedWithin(ctx, testCID, ns, pinPropagationWindow)
		if err != nil {
			t.Fatal(err)
		}
		return ok
	}
	if recent() {
		t.Fatal("an hour-old pin request reads as recent")
	}

	var firstUpload string
	_ = db.db.QueryRow(`SELECT uploaded_at FROM ipfs_content_ownership`).Scan(&firstUpload)

	if err := h.recordCIDOwnership(ctx, testCID, ns, "memo.m4a", ns, 10); err != nil {
		t.Fatalf("re-upload: %v", err)
	}
	if !recent() {
		t.Error("a re-upload did not count as a fresh pin request")
	}
	var afterReupload string
	_ = db.db.QueryRow(`SELECT uploaded_at FROM ipfs_content_ownership`).Scan(&afterReupload)
	if afterReupload != firstUpload {
		t.Errorf("uploaded_at moved from %s to %s; it is the first upload", firstUpload, afterReupload)
	}

	_, _ = db.db.Exec(`UPDATE ipfs_content_ownership SET pin_requested_at = datetime('now','-1 hour')`)
	if err := h.updatePinStatus(ctx, testCID, ns, true); err != nil {
		t.Fatal(err)
	}
	if !recent() {
		t.Error("a pin did not count as a fresh pin request")
	}
}

// A row written by a gateway that predates migration 058 has no
// pin_requested_at; its upload time stands in.
func TestPinRequestedWithin_fallsBackToUploadTime(t *testing.T) {
	db := ownershipSchema(t)
	h := New(&mockIPFSClient{}, newTestLogger(), Config{}, db, nil)
	if _, err := db.db.Exec(`INSERT INTO ipfs_content_ownership (id, cid, namespace, is_pinned, uploaded_at, uploaded_by)
		VALUES ('x', ?, 'anchat', 1, datetime('now'), 'anchat')`, testCID); err != nil {
		t.Fatal(err)
	}
	ok, err := h.pinRequestedWithin(context.Background(), testCID, "anchat", pinPropagationWindow)
	if err != nil || !ok {
		t.Fatalf("pinRequestedWithin = %v, %v; want the fresh upload time to count", ok, err)
	}
}
