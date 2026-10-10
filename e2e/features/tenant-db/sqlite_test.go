//go:build e2e_fleet

package tenantdb

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/e2e/features/internal/tenancy"
	"github.com/DeBrosOfficial/network/e2e/harness"
	"github.com/DeBrosOfficial/network/e2e/harness/fleet"
	"github.com/DeBrosOfficial/network/e2e/harness/gw"
	"github.com/DeBrosOfficial/network/e2e/harness/ns"
)

// Per-application SQLite routes (docs/whitepaper/technical-reference/appendices/i-api-surface.md "Application databases");
// shapes are core/pkg/gateway/handlers/sqlite.
const (
	pathSQLCreate  = "/v1/db/sqlite/create"
	pathSQLQuery   = "/v1/db/sqlite/query"
	pathSQLList    = "/v1/db/sqlite/list"
	pathSQLDelete  = "/v1/db/sqlite/delete"
	pathSQLBackup  = "/v1/db/sqlite/backup"
	pathSQLBackups = "/v1/db/sqlite/backups"
	// sqliteBase is where a node keeps tenant databases:
	// <oramaDir>/data/sqlite/<ns>/<db>.db (website/src/docs/contributor/architecture-reference.mdx "What a gateway writes").
	sqliteBase = "/opt/orama/.orama/data/sqlite"
	// cleanupBudget bounds one cleanup call.
	cleanupBudget = time.Minute
)

type sqlResult struct {
	Columns      []string `json:"columns"`
	Rows         [][]any  `json:"rows"`
	RowsAffected int64    `json:"rows_affected"`
	Error        string   `json:"error"`
}

func sqlCreate(t testing.TB, n *ns.Namespace, name string) {
	t.Helper()
	tenancy.Post(t, n.Client, pathSQLCreate, tenancy.Owner(n), map[string]string{"database_name": name}).Expect(t, http.StatusCreated)
	t.Cleanup(func() {
		// Deleted with the namespace; deleting first proves the files go too.
		ctx, cancel := context.WithTimeout(context.Background(), cleanupBudget)
		defer cancel()
		r, err := n.Client.Send(ctx, deleteReq(n, name))
		if err == nil && r.Status != http.StatusOK && r.Status != http.StatusNotFound {
			t.Errorf("cleanup: deleting database %s answered %d", name, r.Status)
		}
	})
}

func sqlQuery(t testing.TB, n *ns.Namespace, name, query string, params ...any) (*sqlResult, int) {
	t.Helper()
	var out sqlResult
	r := tenancy.Post(t, n.Client, pathSQLQuery, tenancy.Owner(n), map[string]any{"database_name": name, "query": query, "params": nonNil(params)})
	if err := r.Decode(&out); err != nil {
		t.Fatalf("sqlite query answered %d with no JSON: %v", r.Status, err)
	}
	return &out, r.Status
}

// homeNode finds the node that holds the database file.
func homeNode(t testing.TB, f *fleet.Fleet, n *ns.Namespace, name string) fleet.Node {
	t.Helper()
	var homes []fleet.Node
	for _, node := range f.State.Nodes {
		if f.Exec(t, node, "test -f "+dbFile(n, name)).Exit == 0 {
			homes = append(homes, node)
		}
	}
	if len(homes) != 1 {
		t.Fatalf("database %s is on %d nodes, want exactly one", name, len(homes))
	}
	return homes[0]
}

func dbFile(n *ns.Namespace, name string) string {
	return sqliteBase + "/" + n.Name + "/" + name + ".db"
}

func TestSQLite_createQueryListDelete(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	n := tenancy.Namespace(t, f, ns.Options{})
	sqlCreate(t, n, "app_main")
	tenancy.Post(t, n.Client, pathSQLCreate, tenancy.Owner(n), map[string]string{"database_name": "app_main"}).Expect(t, http.StatusConflict)
	if _, st := sqlQuery(t, n, "app_main", "CREATE TABLE notes (id INTEGER PRIMARY KEY, body TEXT)"); st != http.StatusOK {
		t.Fatalf("create table answered %d", st)
	}
	if res, st := sqlQuery(t, n, "app_main", "INSERT INTO notes (body) VALUES (?)", "héllo \u202e"); st != http.StatusOK || res.RowsAffected != 1 {
		t.Fatalf("insert answered %d %+v", st, res)
	}
	res, st := sqlQuery(t, n, "app_main", "SELECT body FROM notes")
	if st != http.StatusOK || len(res.Rows) != 1 || res.Rows[0][0] != "héllo \u202e" {
		t.Fatalf("select answered %d %+v", st, res)
	}
	var list struct {
		Databases []struct {
			DatabaseName string `json:"database_name"`
		} `json:"databases"`
	}
	if err := tenancy.Send(t, n.Client, http.MethodGet, pathSQLList, tenancy.Owner(n), nil).Expect(t, http.StatusOK).Decode(&list); err != nil {
		t.Fatal(err)
	}
	if len(list.Databases) != 1 || list.Databases[0].DatabaseName != "app_main" {
		t.Fatalf("list returned %+v", list.Databases)
	}
	home := homeNode(t, f, n, "app_main")
	n.Client.MustSend(t, deleteReq(n, "app_main")).Expect(t, http.StatusOK)
	if f.Exec(t, home, "ls "+dbFile(n, "app_main")+"*").Exit == 0 {
		t.Fatalf("%s still holds the deleted database's files", home.Name)
	}
	n.Client.MustSend(t, deleteReq(n, "app_main")).Expect(t, http.StatusNotFound)
	if _, st := sqlQuery(t, n, "app_main", "SELECT 1"); st != http.StatusNotFound {
		t.Fatalf("a deleted database answered a query with %d", st)
	}
}

// TestSQLite_filesArePrivate: 0600 files in 0700 directories, owned by the
// gateway's user (core/pkg/gateway/handlers/sqlite/create_handler.go).
func TestSQLite_filesArePrivate(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	n := tenancy.Namespace(t, f, ns.Options{})
	sqlCreate(t, n, "private")
	sqlQuery(t, n, "private", "CREATE TABLE t (x)")
	home := homeNode(t, f, n, "private")
	for path, want := range map[string]string{
		sqliteBase: "700 orama", sqliteBase + "/" + n.Name: "700 orama", dbFile(n, "private"): "600 orama",
	} {
		if got := strings.TrimSpace(f.MustExec(t, home, "stat -c '%a %U' "+path).Stdout); got != want {
			t.Errorf("%s: %s is %q, want %q", home.Name, path, got, want)
		}
	}
	out := f.Exec(t, home, "runuser -u nobody -- cat "+dbFile(n, "private"))
	if out.Exit == 0 {
		t.Fatalf("another user on %s read the tenant database", home.Name)
	}
}

// TestSQLite_everyNodeServesTheHomeNodesFile: a request reaching any node's
// gateway is forwarded to the database's home node (handlers/sqlite/forward.go).
func TestSQLite_everyNodeServesTheHomeNodesFile(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	n := tenancy.Namespace(t, f, ns.Options{})
	sqlCreate(t, n, "fwd")
	sqlQuery(t, n, "fwd", "CREATE TABLE t (who TEXT)")
	for _, nc := range tenancy.PerNode(t, f, n.Client) {
		body := map[string]any{"database_name": "fwd", "query": "INSERT INTO t VALUES (?)", "params": []any{nc.Node.Name}}
		tenancy.Post(t, nc.Client, pathSQLQuery, tenancy.Owner(n), body).Expect(t, http.StatusOK)
	}
	for _, nc := range tenancy.PerNode(t, f, n.Client) {
		var res sqlResult
		body := map[string]any{"database_name": "fwd", "query": "SELECT COUNT(*) FROM t", "params": []any{}}
		if err := tenancy.Post(t, nc.Client, pathSQLQuery, tenancy.Owner(n), body).Expect(t, http.StatusOK).Decode(&res); err != nil {
			t.Fatal(err)
		}
		if len(res.Rows) != 1 || res.Rows[0][0] != float64(len(f.State.Nodes)) {
			t.Errorf("%s sees %v rows, want one per node", nc.Node.Name, res.Rows)
		}
	}
}

// TestSQLiteInput_crossDatabaseSQLRefused: ATTACH/DETACH and more than one
// statement are refused (bugboard #252; handlers/sqlite/tenant_open.go).
func TestSQLiteInput_crossDatabaseSQLRefused(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	pair := tenancy.Namespaces(t, f, 2, ns.Options{})
	a, b := pair[0], pair[1]
	sqlCreate(t, a, "mine")
	sqlCreate(t, b, "theirs")
	theirs := dbFile(b, "theirs")
	for _, q := range []string{
		"ATTACH DATABASE '" + theirs + "' AS x", "attach '" + theirs + "' as x", "/**/ATTACH/**/'" + theirs + "'/**/AS x",
		"DETACH DATABASE main", "SELECT 1; ATTACH '" + theirs + "' AS x", "SELECT 1; SELECT 2",
		"VACUUM INTO '" + sqliteBase + "/" + b.Name + "/planted.db'",
	} {
		if _, st := sqlQuery(t, a, "mine", q); st == http.StatusOK {
			t.Errorf("%q was run", q)
		}
	}
	home := homeNode(t, f, b, "theirs")
	if f.Exec(t, home, "test -e "+sqliteBase+"/"+b.Name+"/planted.db").Exit == 0 {
		t.Fatal("A's SQL wrote a file into B's database directory")
	}
	// A literal containing the word is data, not a command.
	sqlQuery(t, a, "mine", "CREATE TABLE w (v TEXT)")
	if _, st := sqlQuery(t, a, "mine", "INSERT INTO w VALUES ('please ATTACH this; now')"); st != http.StatusOK {
		t.Fatalf("a string literal mentioning ATTACH was refused: %d", st)
	}
}

func TestSQLiteInput_namesValidated(t *testing.T) {
	t.Parallel()
	n := tenancy.Namespace(t, harness.Fleet(t), ns.Options{})
	for _, name := range []string{"", "../escape", "a/b", "a.b", "x.db", "name with space", "ünï", "a\x00b", strings.Repeat("n", 65), "..", "%2e%2e"} {
		if r := tenancy.Post(t, n.Client, pathSQLCreate, tenancy.Owner(n), map[string]string{"database_name": name}); r.Status != http.StatusBadRequest {
			t.Errorf("create %q: want 400, got %d", name, r.Status)
		}
		if _, st := sqlQuery(t, n, name, "SELECT 1"); st != http.StatusBadRequest && st != http.StatusNotFound {
			t.Errorf("query %q: want 400/404, got %d", name, st)
		}
	}
	sqlCreate(t, n, strings.Repeat("n", 64))
	tenancy.Post(t, n.Client, pathSQLCreate, tenancy.Owner(n), []byte("{")).Expect(t, http.StatusBadRequest)
	tenancy.Send(t, n.Client, http.MethodGet, pathSQLDelete, tenancy.Owner(n), nil).Expect(t, http.StatusMethodNotAllowed)
	if _, st := sqlQuery(t, n, "missing", "SELECT 1"); st != http.StatusNotFound {
		t.Fatalf("an unknown database answered %d", st)
	}
}

func TestSQLite_backupListedWithCID(t *testing.T) {
	t.Parallel()
	n := tenancy.Namespace(t, harness.Fleet(t), ns.Options{})
	sqlCreate(t, n, "backed")
	sqlQuery(t, n, "backed", "CREATE TABLE t (x)")
	var bk struct {
		BackupCID string `json:"backup_cid"`
	}
	if err := tenancy.Post(t, n.Client, pathSQLBackup, tenancy.Owner(n), map[string]string{"database_name": "backed"}).Expect(t, http.StatusOK).Decode(&bk); err != nil || bk.BackupCID == "" {
		t.Fatalf("backup returned no CID: %v", err)
	}
	r := tenancy.Send(t, n.Client, http.MethodGet, pathSQLBackups+"?database_name=backed", tenancy.Owner(n), nil).Expect(t, http.StatusOK)
	if !strings.Contains(string(r.Body), bk.BackupCID) {
		t.Fatalf("backups does not list %s: %s", bk.BackupCID, r.Body)
	}
	tenancy.Send(t, n.Client, http.MethodGet, pathSQLBackups, tenancy.Owner(n), nil).Expect(t, http.StatusBadRequest)
	tenancy.Send(t, n.Client, http.MethodGet, pathSQLBackups+"?database_name=none", tenancy.Owner(n), nil).Expect(t, http.StatusNotFound)
	tenancy.Post(t, n.Client, pathSQLBackup, tenancy.Owner(n), map[string]string{"database_name": "none"}).Expect(t, http.StatusNotFound)
}

func TestSQLiteIsolation_otherNamespaceRefused(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	pair := tenancy.Namespaces(t, f, 2, ns.Options{})
	a, b := pair[0], pair[1]
	sqlCreate(t, a, "shared_name")
	body := map[string]any{"database_name": "shared_name", "query": "SELECT 1", "params": []any{}}
	tenancy.ExpectDenied(t, tenancy.Post(t, a.Client, pathSQLQuery, tenancy.Owner(b), body), "B's session on A's SQLite")
	if r := tenancy.Post(t, b.Client, pathSQLQuery, tenancy.Owner(b), body); r.Status != http.StatusNotFound {
		t.Fatalf("B querying a name only A created answered %d", r.Status)
	}
	for _, who := range []tenancy.Cred{{}, {APIKey: tenancy.APIKey(t, a, "cache")}} {
		if r := tenancy.Post(t, a.Client, pathSQLQuery, who, body); r.Status != http.StatusUnauthorized && r.Status != http.StatusForbidden {
			t.Errorf("SQLite query without the db grant answered %d", r.Status)
		}
	}
}

func deleteReq(n *ns.Namespace, name string) gw.Req {
	body, _ := json.Marshal(map[string]string{"database_name": name})
	return gw.Req{Method: http.MethodPost, Path: pathSQLDelete, Bearer: n.Owner.Token(),
		Header: http.Header{"Content-Type": {"application/json"}}, Body: body}
}
