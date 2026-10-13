package rqlite

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	_ "github.com/mattn/go-sqlite3"
)

func columnsGateway(t *testing.T) *HTTPGateway {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec(`CREATE TABLE t (id INTEGER PRIMARY KEY, name TEXT, zed TEXT); INSERT INTO t VALUES (1, 'a', 'z')`); err != nil {
		t.Fatal(err)
	}
	return NewHTTPGateway(NewClient(db), "/v1/rqlite")
}

func queryGateway(t *testing.T, g *HTTPGateway, body string) map[string]any {
	t.Helper()
	rec := httptest.NewRecorder()
	g.handleQuery(rec, httptest.NewRequest(http.MethodPost, "/v1/rqlite/query", strings.NewReader(body)))
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

// A row is a JSON object, whose keys have no order; a caller that indexes rows
// by position (the Go SDK's QueryResult.Rows) needs the statement's column
// order, so the response carries it.
func TestHandleQuery_reportsColumnsInSelectOrder(t *testing.T) {
	g := columnsGateway(t)

	out := queryGateway(t, g, `{"sql":"SELECT zed, id, name FROM t"}`)
	if got := out["columns"]; !reflect.DeepEqual(got, []any{"zed", "id", "name"}) {
		t.Errorf("columns = %v, want the SELECT order", got)
	}
	if out["count"] != float64(1) {
		t.Errorf("count = %v", out["count"])
	}
}

func TestHandleQuery_noRowsStillNamesTheColumns(t *testing.T) {
	g := columnsGateway(t)

	out := queryGateway(t, g, `{"sql":"SELECT name, id FROM t WHERE id = ?","args":[99]}`)
	if got := out["columns"]; !reflect.DeepEqual(got, []any{"name", "id"}) {
		t.Errorf("columns = %v", got)
	}
	items, ok := out["items"].([]any)
	if !ok || len(items) != 0 {
		t.Errorf("items = %#v, want an empty array, not null", out["items"])
	}
}
