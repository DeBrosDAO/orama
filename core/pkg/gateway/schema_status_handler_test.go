package gateway

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/DeBrosOfficial/network/migrations"
	_ "github.com/mattn/go-sqlite3"
)

// openTestSQLDB creates an in-memory SQLite with the schema_migrations
// table seeded.
func openTestSQLDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec(`
		CREATE TABLE schema_migrations (
			version INTEGER PRIMARY KEY,
			applied_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
		)`); err != nil {
		t.Fatalf("seed: %v", err)
	}
	return db
}

func TestSchemaStatus_in_sync(t *testing.T) {
	db := openTestSQLDB(t)
	// Seed schema_migrations to the binary's required version.
	for v := 1; v <= migrations.RequiredVersion(); v++ {
		if _, err := db.Exec("INSERT INTO schema_migrations (version) VALUES (?)", v); err != nil {
			t.Fatalf("seed v=%d: %v", v, err)
		}
	}
	g := &Gateway{sqlDB: db}

	req := httptest.NewRequest(http.MethodGet, "/v1/schema-status", nil)
	rec := httptest.NewRecorder()
	g.handleSchemaStatus(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var resp schemaStatusResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !resp.OK {
		t.Error("ok must be true")
	}
	if !resp.InSync {
		t.Error("in_sync must be true when applied == required")
	}
	if resp.AppliedVersion != migrations.RequiredVersion() {
		t.Errorf("applied = %d, want %d", resp.AppliedVersion, migrations.RequiredVersion())
	}
	if len(resp.Pending) != 0 {
		t.Errorf("pending should be empty when in sync, got %d", len(resp.Pending))
	}
}

func TestSchemaStatus_lagging(t *testing.T) {
	db := openTestSQLDB(t)
	// Seed only the first migration; the rest are "pending".
	if _, err := db.Exec("INSERT INTO schema_migrations (version) VALUES (1)"); err != nil {
		t.Fatalf("seed: %v", err)
	}
	g := &Gateway{sqlDB: db}

	req := httptest.NewRequest(http.MethodGet, "/v1/schema-status", nil)
	rec := httptest.NewRecorder()
	g.handleSchemaStatus(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (drift is reported via in_sync, not via HTTP error)", rec.Code)
	}
	var resp schemaStatusResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.InSync {
		t.Error("in_sync must be false when behind")
	}
	if resp.AppliedVersion != 1 {
		t.Errorf("applied = %d, want 1", resp.AppliedVersion)
	}
	if len(resp.Pending) == 0 {
		t.Error("expected non-empty pending list when behind")
	}
}

func TestSchemaStatus_no_db_returns_envelope(t *testing.T) {
	g := &Gateway{sqlDB: nil}
	req := httptest.NewRequest(http.MethodGet, "/v1/schema-status", nil)
	rec := httptest.NewRecorder()
	g.handleSchemaStatus(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", rec.Code)
	}
	// Confirm canonical envelope (bug #212 contract).
	var body map[string]interface{}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if ok, _ := body["ok"].(bool); ok {
		t.Error("ok must be false on error")
	}
	errObj, ok := body["error"].(map[string]interface{})
	if !ok {
		t.Fatal("error must be an object")
	}
	if msg, _ := errObj["message"].(string); msg == "" {
		t.Error("error message must be populated")
	}
}

func TestSchemaStatus_method_not_allowed(t *testing.T) {
	g := &Gateway{sqlDB: openTestSQLDB(t)}
	req := httptest.NewRequest(http.MethodPost, "/v1/schema-status", nil)
	rec := httptest.NewRecorder()
	g.handleSchemaStatus(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want 405", rec.Code)
	}
}
