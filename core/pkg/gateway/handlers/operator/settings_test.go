package operator

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/migrations"
	"github.com/DeBrosOfficial/network/pkg/client"
	"github.com/DeBrosOfficial/network/pkg/gateway/auth"
	"github.com/DeBrosOfficial/network/pkg/rqlite"
	_ "github.com/mattn/go-sqlite3"
	"go.uber.org/zap"
)

// auditCapture is the registry the real AuditLog writes to. The handler does
// not grow a second logger; this is the insert AuditLog.Record sends.
type auditCapture struct {
	client.DatabaseClient
	rows [][]any
}

func (a *auditCapture) Query(_ context.Context, query string, args ...any) (*client.QueryResult, error) {
	if !strings.Contains(query, "INSERT INTO audit_events") {
		return nil, errString("unexpected audit query: " + query)
	}
	copied := make([]any, len(args))
	copy(copied, args)
	a.rows = append(a.rows, copied)
	return &client.QueryResult{Count: 1}, nil
}

func settingsDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := rqlite.ApplyEmbeddedMigrations(t.Context(), db, migrations.FS, zap.NewNop()); err != nil {
		t.Fatalf("apply migrations: %v", err)
	}
	return db
}

func settingsHandler(t *testing.T, wallets ...string) (*Handler, *sql.DB, *auditCapture) {
	t.Helper()
	db := settingsDB(t)
	for _, w := range wallets {
		if _, err := db.Exec(`INSERT INTO operators (wallet, added_by) VALUES (?, 'test')`, strings.ToLower(w)); err != nil {
			t.Fatal(err)
		}
	}
	cap := &auditCapture{}
	h := NewHandler(zap.NewNop(), rqlite.NewClient(db))
	h.SetAuditLog(auth.NewAuditLog(func() client.DatabaseClient { return cap }, nil))
	return h, db, cap
}

func putJSON(method, path, wallet, body string) *http.Request {
	r := walletRequest(method, path, wallet)
	r.Body = jsonBody(body)
	return r
}

func TestSettings_aNewClusterShowsOperatorsAndTheDefaultCap(t *testing.T) {
	h, _, audit := settingsHandler(t, "0xoperator")
	w := httptest.NewRecorder()
	h.HandleSettings(w, walletRequest(http.MethodGet, "/v1/operator/settings", "0xoperator"))
	if w.Code != http.StatusOK {
		t.Fatalf("status %d %s", w.Code, w.Body.String())
	}
	var resp struct {
		Mode string `json:"namespace_creation"`
		Cap  int    `json:"max_namespaces_per_wallet"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Mode != CreationOperators || resp.Cap != DefaultMaxNamespacesPerWallet {
		t.Fatalf("settings = %+v", resp)
	}
	if len(audit.rows) != 0 {
		t.Fatalf("reading settings wrote %d audit rows", len(audit.rows))
	}
}

func TestSettings_setIsAuditedAndABadValueIsNotStored(t *testing.T) {
	h, db, audit := settingsHandler(t, "0xoperator")

	bad := httptest.NewRecorder()
	h.HandleSettings(bad, putJSON(http.MethodPut, "/v1/operator/settings/namespace-creation", "0xoperator", `{"value":"everyone"}`))
	if bad.Code != http.StatusBadRequest {
		t.Fatalf("bad mode: %d %s", bad.Code, bad.Body.String())
	}
	if n := settingCount(t, db, SettingNamespaceCreation); n != 0 {
		t.Fatalf("a rejected mode was stored (%d rows)", n)
	}
	if len(audit.rows) != 0 {
		t.Fatal("a rejected change was audited as if it happened")
	}

	ok := httptest.NewRecorder()
	h.HandleSettings(ok, putJSON(http.MethodPut, "/v1/operator/settings/namespace-creation", "0xoperator", `{"value":"allowlist"}`))
	if ok.Code != http.StatusOK {
		t.Fatalf("set: %d %s", ok.Code, ok.Body.String())
	}
	got := settingValue(t, db, SettingNamespaceCreation)
	if got != CreationAllowlist {
		t.Fatalf("stored %q", got)
	}
	if len(audit.rows) != 1 || audit.rows[0][2] != auth.AuditOperatorAction || audit.rows[0][3] != "cluster_settings" {
		t.Fatalf("audit %#v", audit.rows)
	}
	meta, _ := audit.rows[0][7].(string)
	if !strings.Contains(meta, `"value":"allowlist"`) || !strings.Contains(meta, `"key":"namespace_creation"`) {
		t.Fatalf("metadata %q", meta)
	}

	cap := httptest.NewRecorder()
	h.HandleSettings(cap, putJSON(http.MethodPut, "/v1/operator/settings/max-namespaces-per-wallet", "0xoperator", `{"value":11}`))
	if cap.Code != http.StatusOK {
		t.Fatalf("cap: %d %s", cap.Code, cap.Body.String())
	}
	if settingValue(t, db, SettingMaxNamespacesPerWallet) != "11" {
		t.Fatalf("cap stored as %q", settingValue(t, db, SettingMaxNamespacesPerWallet))
	}
	if len(audit.rows) != 2 {
		t.Fatalf("%d audit rows, want the cap change too", len(audit.rows))
	}

	for _, body := range []string{`{"value":0}`, `{"value":10001}`, `{"value":"nope"}`} {
		w := httptest.NewRecorder()
		h.HandleSettings(w, putJSON(http.MethodPut, "/v1/operator/settings/max-namespaces-per-wallet", "0xoperator", body))
		if w.Code != http.StatusBadRequest {
			t.Errorf("%s: %d %s", body, w.Code, w.Body.String())
		}
	}
	if settingValue(t, db, SettingMaxNamespacesPerWallet) != "11" {
		t.Fatal("a rejected cap replaced the stored one")
	}
}

func TestCreators_addAndRemoveAreAudited(t *testing.T) {
	h, db, audit := settingsHandler(t, "0xoperator")
	const upper = "0xAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
	const lower = "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

	add := httptest.NewRecorder()
	h.HandleCreators(add, putJSON(http.MethodPost, "/v1/operator/creators", "0xoperator", `{"wallet":"`+upper+`"}`))
	if add.Code != http.StatusOK || bodyWallet(t, add) != lower {
		t.Fatalf("add: %d %s", add.Code, add.Body.String())
	}
	// A second add is the same row, and still an operator act.
	again := httptest.NewRecorder()
	h.HandleCreators(again, putJSON(http.MethodPost, "/v1/operator/creators", "0xoperator", `{"wallet":"`+lower+`"}`))
	if again.Code != http.StatusOK {
		t.Fatalf("second add: %d %s", again.Code, again.Body.String())
	}
	if n := countCreators(t, db); n != 1 {
		t.Fatalf("creators = %d, want 1", n)
	}

	list := httptest.NewRecorder()
	h.HandleCreators(list, walletRequest(http.MethodGet, "/v1/operator/creators", "0xoperator"))
	if list.Code != http.StatusOK || !strings.Contains(list.Body.String(), lower) {
		t.Fatalf("list: %d %s", list.Code, list.Body.String())
	}

	rm := httptest.NewRecorder()
	h.HandleCreators(rm, walletRequest(http.MethodDelete, "/v1/operator/creators/"+upper, "0xoperator"))
	if rm.Code != http.StatusOK || bodyWallet(t, rm) != lower {
		t.Fatalf("remove: %d %s", rm.Code, rm.Body.String())
	}
	if n := countCreators(t, db); n != 0 {
		t.Fatalf("creators left: %d", n)
	}

	missing := httptest.NewRecorder()
	h.HandleCreators(missing, walletRequest(http.MethodDelete, "/v1/operator/creators/"+lower, "0xoperator"))
	if missing.Code != http.StatusNotFound {
		t.Fatalf("missing: %d %s", missing.Code, missing.Body.String())
	}

	if len(audit.rows) != 3 {
		t.Fatalf("%d audit rows, want add, add, remove", len(audit.rows))
	}
	for i, wantOp := range []string{"add", "add", "remove"} {
		if audit.rows[i][2] != auth.AuditOperatorAction || audit.rows[i][3] != "namespace_creators" {
			t.Fatalf("row %d: %#v", i, audit.rows[i])
		}
		meta, _ := audit.rows[i][7].(string)
		if !strings.Contains(meta, `"op":"`+wantOp+`"`) || !strings.Contains(meta, lower) {
			t.Fatalf("row %d metadata %q", i, meta)
		}
	}
}

func TestSettingsAndCreators_aNonOperatorIsRefused(t *testing.T) {
	h, db, audit := settingsHandler(t, "0xoperator")
	for _, call := range []struct {
		method, path, body string
		fn                 func(http.ResponseWriter, *http.Request)
	}{
		{http.MethodPut, "/v1/operator/settings/namespace-creation", `{"value":"open"}`, h.HandleSettings},
		{http.MethodPost, "/v1/operator/creators", `{"wallet":"0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}`, h.HandleCreators},
	} {
		w := httptest.NewRecorder()
		call.fn(w, putJSON(call.method, call.path, "0xstranger", call.body))
		if w.Code != http.StatusForbidden {
			t.Errorf("%s %s: %d %s", call.method, call.path, w.Code, w.Body.String())
		}
	}
	if n := settingCount(t, db, SettingNamespaceCreation); n != 0 {
		t.Fatalf("a non-operator stored a setting (%d)", n)
	}
	if n := countCreators(t, db); n != 0 {
		t.Fatalf("a non-operator added a creator (%d)", n)
	}
	if len(audit.rows) != 0 {
		t.Fatalf("a refusal was audited (%d rows)", len(audit.rows))
	}
}

func TestCreators_aMistypedAddressIsRefused(t *testing.T) {
	h, db, _ := settingsHandler(t, "0xoperator")
	w := httptest.NewRecorder()
	h.HandleCreators(w, putJSON(http.MethodPost, "/v1/operator/creators", "0xoperator", `{"wallet":"0xnot-an-address"}`))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status %d %s", w.Code, w.Body.String())
	}
	if n := countCreators(t, db); n != 0 {
		t.Fatalf("a rejected wallet was stored (%d)", n)
	}
}

func settingValue(t *testing.T, db *sql.DB, key string) string {
	t.Helper()
	var value string
	if err := db.QueryRow(`SELECT value FROM cluster_settings WHERE key = ?`, key).Scan(&value); err != nil {
		t.Fatalf("read %s: %v", key, err)
	}
	return value
}

func settingCount(t *testing.T, db *sql.DB, key string) int {
	t.Helper()
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM cluster_settings WHERE key = ?`, key).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func countCreators(t *testing.T, db *sql.DB) int {
	t.Helper()
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM namespace_creators`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}
