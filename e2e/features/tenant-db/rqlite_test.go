//go:build e2e_fleet

package tenantdb

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/features/internal/tenancy"
	"github.com/DeBrosOfficial/network/e2e/harness"
	"github.com/DeBrosOfficial/network/e2e/harness/gw"
	"github.com/DeBrosOfficial/network/e2e/harness/ns"
)

// Namespace RQLite routes (docs/whitepaper/technical-reference/appendices/i-api-surface.md "Database (RQLite)"); shapes
// are core/pkg/rqlite/gateway.go.
const (
	pathCreateTable = "/v1/rqlite/create-table"
	pathDropTable   = "/v1/rqlite/drop-table"
	pathExec        = "/v1/rqlite/exec"
	pathQuery       = "/v1/rqlite/query"
	pathFind        = "/v1/rqlite/find"
	pathFindOne     = "/v1/rqlite/find-one"
	pathSelect      = "/v1/rqlite/select"
	pathSchema      = "/v1/rqlite/schema"
	pathTx          = "/v1/rqlite/transaction"
	pathSchemaState = "/v1/schema-status"
	// peopleDDL is the table most tests use.
	peopleDDL = `CREATE TABLE people (id INTEGER PRIMARY KEY, name TEXT NOT NULL UNIQUE, age INTEGER)`
)

type rows struct {
	Items []map[string]any `json:"items"`
	Count int              `json:"count"`
}

// db is one namespace's database as its owner sees it.
type db struct {
	t   testing.TB
	n   *ns.Namespace
	who tenancy.Cred
	c   *gw.Client
}

func ownerDB(t testing.TB, n *ns.Namespace) *db {
	return &db{t: t, n: n, who: tenancy.Owner(n), c: n.Client}
}

func (d *db) call(path string, body any) *gw.Response {
	d.t.Helper()
	return tenancy.Post(d.t, d.c, path, d.who, body)
}

func (d *db) exec(sql string, args ...any) *gw.Response {
	d.t.Helper()
	return d.call(pathExec, map[string]any{"sql": sql, "args": nonNil(args)})
}

func (d *db) query(sql string, args ...any) rows {
	d.t.Helper()
	var out rows
	if err := d.call(pathQuery, map[string]any{"sql": sql, "args": nonNil(args)}).Expect(d.t, http.StatusOK).Decode(&out); err != nil {
		d.t.Fatal(err)
	}
	if out.Count != len(out.Items) {
		d.t.Errorf("query count %d but %d items", out.Count, len(out.Items))
	}
	return out
}

func nonNil(args []any) []any {
	if args == nil {
		return []any{}
	}
	return args
}

// people creates the people table with three rows.
func (d *db) people() {
	d.t.Helper()
	d.call(pathCreateTable, map[string]any{"schema": peopleDDL}).Expect(d.t, http.StatusCreated)
	for i, name := range []string{"ada", "bob", "cy"} {
		d.exec(`INSERT INTO people (name, age) VALUES (?, ?)`, name, 30+i).Expect(d.t, http.StatusOK)
	}
}

func TestRQLite_createExecQueryDrop(t *testing.T) {
	t.Parallel()
	d := ownerDB(t, tenancy.Namespace(t, harness.Fleet(t), ns.Options{}))
	d.people()
	var res struct {
		RowsAffected int64 `json:"rows_affected"`
		LastInsertID int64 `json:"last_insert_id"`
	}
	if err := d.exec(`UPDATE people SET age = age + 1 WHERE age >= ?`, 31).Expect(t, http.StatusOK).Decode(&res); err != nil {
		t.Fatal(err)
	}
	if res.RowsAffected != 2 {
		t.Fatalf("update affected %d rows, want 2", res.RowsAffected)
	}
	got := d.query(`SELECT name, age FROM people ORDER BY name`)
	want := []string{"ada:30", "bob:32", "cy:33"}
	if len(got.Items) != len(want) {
		t.Fatalf("the table has %d rows %v, want %v", len(got.Items), got.Items, want)
	}
	for i, r := range got.Items {
		if s := fmt.Sprintf("%v:%v", r["name"], r["age"]); i >= len(want) || s != want[i] {
			t.Fatalf("row %d is %s, want %v", i, s, want)
		}
	}
	d.call(pathDropTable, map[string]any{"table": "people"}).Expect(t, http.StatusOK)
	d.call(pathDropTable, map[string]any{"table": "people"}).Expect(t, http.StatusNotFound)
	if r := d.call(pathQuery, map[string]any{"sql": "SELECT * FROM people"}); r.Status < 400 {
		t.Fatalf("the dropped table still answers: %d", r.Status)
	}
}

func TestRQLite_findFindOneSelect(t *testing.T) {
	t.Parallel()
	d := ownerDB(t, tenancy.Namespace(t, harness.Fleet(t), ns.Options{}))
	d.people()
	var found rows
	if err := d.call(pathFind, map[string]any{"table": "people", "criteria": map[string]any{"age": 31}}).Expect(t, http.StatusOK).Decode(&found); err != nil {
		t.Fatal(err)
	}
	if found.Count != 1 || found.Items[0]["name"] != "bob" {
		t.Fatalf("find age=31 returned %+v", found)
	}
	var one map[string]any
	if err := d.call(pathFindOne, map[string]any{"table": "people", "criteria": map[string]any{"name": "cy"}}).Expect(t, http.StatusOK).Decode(&one); err != nil {
		t.Fatal(err)
	}
	if one["age"] != 32.0 {
		t.Fatalf("find-one name=cy returned %v", one)
	}
	d.call(pathFindOne, map[string]any{"table": "people", "criteria": map[string]any{"name": "nobody"}}).Expect(t, http.StatusNotFound)
	var sel rows
	body := map[string]any{"table": "people", "select": []string{"name"},
		"where":    []map[string]any{{"expr": "age > ?", "args": []any{30}}},
		"order_by": []string{"name DESC"}, "limit": 1}
	if err := d.call(pathSelect, body).Expect(t, http.StatusOK).Decode(&sel); err != nil {
		t.Fatal(err)
	}
	if sel.Count != 1 || sel.Items[0]["name"] != "cy" {
		t.Fatalf("select returned %+v, want only cy", sel)
	}
	body["one"], body["where"] = true, []map[string]any{{"expr": "age > ?", "args": []any{99}}}
	d.call(pathSelect, body).Expect(t, http.StatusNotFound)
}

func TestRQLite_schemaListsTables(t *testing.T) {
	t.Parallel()
	d := ownerDB(t, tenancy.Namespace(t, harness.Fleet(t), ns.Options{}))
	d.people()
	var out struct {
		Tables []struct {
			Name string `json:"name"`
			Type string `json:"type"`
			SQL  string `json:"sql"`
		} `json:"tables"`
	}
	if err := tenancy.Send(t, d.c, http.MethodGet, pathSchema, d.who, nil).Expect(t, http.StatusOK).Decode(&out); err != nil {
		t.Fatal(err)
	}
	for _, tb := range out.Tables {
		if tb.Name == "people" && tb.Type == "table" {
			return
		}
	}
	t.Fatalf("schema does not list people: %+v", out.Tables)
}

// TestRQLite_transactionCommitsOrRollsBackWhole: a transaction is atomic
// (docs/whitepaper/technical-reference/appendices/i-api-surface.md db.transaction()): a failing op rolls back every op
// before it, answering 409 with the failing index (core/pkg/rqlite/gateway.go).
func TestRQLite_transactionCommitsOrRollsBackWhole(t *testing.T) {
	t.Parallel()
	d := ownerDB(t, tenancy.Namespace(t, harness.Fleet(t), ns.Options{}))
	d.people()
	ok := map[string]any{"return_results": true, "ops": []map[string]any{
		{"kind": "exec", "sql": "INSERT INTO people (name, age) VALUES (?, ?)", "args": []any{"dee", 40}},
		{"kind": "query", "sql": "SELECT COUNT(*) AS n FROM people"},
	}}
	var res struct {
		Status  string `json:"status"`
		Results []any  `json:"results"`
	}
	if err := d.call(pathTx, ok).Expect(t, http.StatusOK).Decode(&res); err != nil || res.Status != "ok" || len(res.Results) != 2 {
		t.Fatalf("committed transaction answered %+v (%v)", res, err)
	}
	bad := map[string]any{"ops": []map[string]any{
		{"kind": "exec", "sql": "INSERT INTO people (name, age) VALUES (?, ?)", "args": []any{"eve", 50}},
		{"kind": "exec", "sql": "INSERT INTO people (name, age) VALUES (?, ?)", "args": []any{"ada", 1}},
	}}
	var rb struct {
		Status      string `json:"status"`
		FailedIndex int    `json:"failed_index"`
	}
	if err := d.call(pathTx, bad).Expect(t, http.StatusConflict).Decode(&rb); err != nil || rb.Status != "rollback" || rb.FailedIndex != 1 {
		t.Fatalf("failing transaction answered %+v (%v), want rollback at index 1", rb, err)
	}
	if got := d.query(`SELECT name FROM people WHERE name = ?`, "eve"); got.Count != 0 {
		t.Fatal("the op before the failing one was committed")
	}
}

func TestRQLite_transactionLimitsAndShapes(t *testing.T) {
	t.Parallel()
	d := ownerDB(t, tenancy.Namespace(t, harness.Fleet(t), ns.Options{}))
	d.people()
	var many []map[string]any
	for i := range 101 { // MaxBatchOps is 100 (core/pkg/rqlite/batch.go)
		many = append(many, map[string]any{"kind": "exec", "sql": "INSERT INTO people (name) VALUES (?)", "args": []any{fmt.Sprint("n", i)}})
	}
	d.call(pathTx, map[string]any{"ops": many}).Expect(t, http.StatusBadRequest)
	d.call(pathTx, map[string]any{"ops": []map[string]any{{"kind": "drop", "sql": "SELECT 1"}}}).Expect(t, http.StatusBadRequest)
	d.call(pathTx, map[string]any{"ops": []any{}}).Expect(t, http.StatusBadRequest)
	if got := d.query(`SELECT COUNT(*) AS n FROM people`); got.Items[0]["n"] != 3.0 {
		t.Fatalf("refused transactions changed the table: %v", got.Items)
	}
	// The legacy {statements:[...]} form runs as exec ops.
	d.call(pathTx, map[string]any{"statements": []string{"INSERT INTO people (name) VALUES ('legacy')"}}).Expect(t, http.StatusOK)
	if got := d.query(`SELECT name FROM people WHERE name = 'legacy'`); got.Count != 1 {
		t.Fatal("the legacy statements form did not run")
	}
}
