package client

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"

	_ "github.com/mattn/go-sqlite3"

	"github.com/DeBrosOfficial/network/pkg/rqlite"
)

// sqliteBatchClient gives the ORM client the atomic Batch that a production
// gateway gets from its native RQLite connection.
type sqliteBatchClient struct {
	rqlite.Client
	db *sql.DB
}

func (c sqliteBatchClient) Batch(ctx context.Context, ops []rqlite.BatchOp) (*rqlite.BatchResult, error) {
	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	res := &rqlite.BatchResult{Results: make([]rqlite.OpResult, 0, len(ops))}
	for i, op := range ops {
		if _, err := tx.ExecContext(ctx, op.SQL, op.Args...); err != nil {
			_ = tx.Rollback()
			res.Results = append(res.Results, rqlite.OpResult{Kind: op.Kind, Error: err.Error()})
			res.FailedIndex = i
			return res, err
		}
		res.Results = append(res.Results, rqlite.OpResult{Kind: op.Kind})
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	res.Committed = true
	return res, nil
}

// gatewayDB is a real /v1/rqlite API over an in-memory SQLite, behind an
// httptest server that records the Authorization header of every request.
func gatewayDB(t *testing.T, cfg *ClientConfig) (DatabaseClient, *[]string) {
	t.Helper()
	return guardedGatewayDB(t, cfg, nil)
}

func guardedGatewayDB(t *testing.T, cfg *ClientConfig, guard rqlite.SQLGuard) (DatabaseClient, *[]string) {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })

	mux := http.NewServeMux()
	gateway := rqlite.NewHTTPGateway(sqliteBatchClient{Client: rqlite.NewClient(db), db: db}, "/v1/rqlite")
	gateway.SQLGuard = guard
	gateway.RegisterRoutes(mux)
	var mu sync.Mutex
	var bearers []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		bearers = append(bearers, r.Header.Get("Authorization"))
		mu.Unlock()
		mux.ServeHTTP(w, r)
	}))
	t.Cleanup(server.Close)

	cfg.GatewayURL = server.URL
	c := &Client{config: cfg, connected: true}
	c.database = &DatabaseClientImpl{client: c}
	c.gatewayDatabase = newGatewayDatabaseClient(c)
	return c.Database(), &bearers
}

// Bug: Database() dialled RQLite directly, so the Quick Start's configuration
// (a gateway URL and a credential) could not work outside the mesh. A client
// with no DatabaseEndpoints goes through the gateway with its credential.
func TestGatewayDatabase_theDocumentedQueriesWork(t *testing.T) {
	db, bearers := gatewayDB(t, &ClientConfig{AppName: "app", APIKey: "ak_test:app"})
	ctx := context.Background()

	if err := db.CreateTable(ctx, "CREATE TABLE IF NOT EXISTS users (id INTEGER PRIMARY KEY AUTOINCREMENT, name TEXT NOT NULL, email TEXT)"); err != nil {
		t.Fatalf("CreateTable: %v", err)
	}
	const hostile = "Alice'); DROP TABLE users; --"
	res, err := db.Query(ctx, "INSERT INTO users (name, email) VALUES (?, ?)", hostile, "a@example.com")
	if err != nil {
		t.Fatalf("INSERT: %v", err)
	}
	if res.RowsAffected != 1 || res.LastInsertID != 1 {
		t.Errorf("write result = %+v, want 1 row affected and id 1", res)
	}
	if err := db.Transaction(ctx, []string{"INSERT INTO users (name) VALUES ('Bob')", "INSERT INTO users (name) VALUES ('Carol')"}); err != nil {
		t.Fatalf("Transaction: %v", err)
	}

	// Columns in SELECT order, and rows ordered to match them.
	res, err = db.Query(ctx, "SELECT name, id, email FROM users ORDER BY id")
	if err != nil {
		t.Fatalf("SELECT: %v", err)
	}
	if !reflect.DeepEqual(res.Columns, []string{"name", "id", "email"}) || res.Count != 3 {
		t.Fatalf("columns %v, count %d", res.Columns, res.Count)
	}
	if want := []interface{}{hostile, int64(1), "a@example.com"}; !reflect.DeepEqual(res.Rows[0], want) {
		t.Errorf("first row = %#v, want %#v", res.Rows[0], want)
	}
	if res.Rows[1][2] != nil {
		t.Errorf("a NULL came back as %#v", res.Rows[1][2])
	}

	schema, err := db.GetSchema(ctx)
	if err != nil {
		t.Fatalf("GetSchema: %v", err)
	}
	if len(schema.Tables) != 1 || schema.Tables[0].Name != "users" || len(schema.Tables[0].Columns) != 3 {
		t.Fatalf("schema = %+v", schema)
	}
	if name := schema.Tables[0].Columns[1]; name.Name != "name" || name.Nullable {
		t.Errorf("column name = %+v, want a NOT NULL column", name)
	}

	if err := db.DropTable(ctx, "users"); err != nil {
		t.Fatalf("DropTable: %v", err)
	}
	if err := db.DropTable(ctx, "users"); err != nil {
		t.Errorf("dropping a table that is not there: %v", err)
	}

	for _, got := range *bearers {
		if got != "Bearer ak_test:app" {
			t.Errorf("a request carried Authorization %q, want the client's credential", got)
		}
	}
	if len(*bearers) == 0 {
		t.Error("nothing reached the gateway")
	}
}

func TestGatewayDatabase_aFailedStatementRollsTheTransactionBack(t *testing.T) {
	db, _ := gatewayDB(t, &ClientConfig{AppName: "app", JWT: "x.y.z"})
	ctx := context.Background()
	if err := db.CreateTable(ctx, "CREATE TABLE t (id INTEGER PRIMARY KEY)"); err != nil {
		t.Fatal(err)
	}

	err := db.Transaction(ctx, []string{"INSERT INTO t VALUES (1)", "INSERT INTO t VALUES (1)"})
	if err == nil || !strings.Contains(err.Error(), "rolled back at statement 1") {
		t.Fatalf("Transaction = %v, want a rollback naming statement 1", err)
	}
	res, err := db.Query(ctx, "SELECT id FROM t")
	if err != nil || res.Count != 0 {
		t.Errorf("after the rollback: %v rows, %v", res, err)
	}
	if err := db.Transaction(ctx, nil); err != nil {
		t.Errorf("an empty transaction: %v", err)
	}
}

func TestGatewayDatabase_refusesWhatItCannotSend(t *testing.T) {
	db, _ := gatewayDB(t, &ClientConfig{AppName: "app", APIKey: "ak_test:app"})
	ctx := context.Background()

	if _, err := db.Query(ctx, "SELECT * FROM missing"); err == nil || !strings.Contains(err.Error(), "500") {
		t.Errorf("a query the database refuses = %v, want the gateway's status", err)
	}
	if err := db.DropTable(ctx, "bad name; DROP"); err == nil || !strings.Contains(err.Error(), "400") {
		t.Errorf("a bad table identifier = %v, want a 400", err)
	}

	noCredential, _ := gatewayDB(t, &ClientConfig{AppName: "app"})
	if _, err := noCredential.Query(ctx, "SELECT 1"); err == nil || !strings.Contains(err.Error(), "authentication required") {
		t.Errorf("a client with no credential = %v", err)
	}

	unconnected := &Client{config: &ClientConfig{AppName: "app", APIKey: "k"}}
	if _, err := newGatewayDatabaseClient(unconnected).Query(ctx, "SELECT 1"); err == nil || !strings.Contains(err.Error(), "not connected") {
		t.Errorf("a client that is not connected = %v", err)
	}
}

func TestGatewayDatabase_anUnreachableGatewayIsAnError(t *testing.T) {
	c := &Client{config: &ClientConfig{AppName: "app", APIKey: "k", GatewayURL: "http://127.0.0.1:1"}, connected: true}
	_, err := newGatewayDatabaseClient(c).Query(context.Background(), "SELECT 1")
	if err == nil {
		t.Fatal("a query to an unreachable gateway succeeded")
	}
}

// Which transport a client uses follows its configuration.
func TestDatabase_transportFollowsDatabaseEndpoints(t *testing.T) {
	direct, err := NewClient(&ClientConfig{AppName: "app", QuietMode: true, DatabaseEndpoints: []string{"http://10.0.0.1:5001"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := direct.Database().(*DatabaseClientImpl); !ok {
		t.Errorf("a client with DatabaseEndpoints uses %T, want the direct client", direct.Database())
	}
	viaGateway, err := NewClient(DefaultClientConfig("app"))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := viaGateway.Database().(*gatewayDatabaseClient); !ok {
		t.Errorf("a default client uses %T, want the gateway client", viaGateway.Database())
	}
}

func TestDecodeCell(t *testing.T) {
	for raw, want := range map[string]interface{}{
		`1`: int64(1), `-7`: int64(-7), `2.5`: 2.5, `"x"`: "x", `null`: nil, `true`: true, ``: nil,
	} {
		got, err := decodeCell([]byte(raw))
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Errorf("decodeCell(%q) = %#v, %v; want %#v", raw, got, err, want)
		}
	}
	if _, err := decodeCell([]byte(`{`)); err == nil {
		t.Error("a truncated value decoded")
	}
}

// A namespace's database holds the platform's own tables beside the tenant's,
// and the gateway refuses a tenant's SQL on them. They are not part of the
// tenant's schema; any other failure is returned.
func TestGatewayDatabase_schemaLeavesOutTablesTheGatewayRefuses(t *testing.T) {
	guard := func(sql string) error {
		if strings.HasPrefix(sql, "PRAGMA") && strings.Contains(sql, "grants") {
			return fmt.Errorf("names a reserved table")
		}
		return nil
	}
	db, _ := guardedGatewayDB(t, &ClientConfig{AppName: "app", APIKey: "k"}, guard)
	ctx := context.Background()
	for _, table := range []string{"notes", "grants"} {
		if err := db.CreateTable(ctx, "CREATE TABLE "+table+" (id INTEGER)"); err != nil {
			t.Fatal(err)
		}
	}
	schema, err := db.GetSchema(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, table := range schema.Tables {
		names = append(names, table.Name)
	}
	if !reflect.DeepEqual(names, []string{"notes"}) {
		t.Errorf("schema lists %v, want only notes", names)
	}
}

// A redirect is refused, never followed: Go forwards X-API-Key on a
// cross-host hop, so following one could hand the key to another host
// (security review, 2026-09-30).
func TestGatewayDatabase_aRedirectIsRefusedNotFollowed(t *testing.T) {
	followed := false
	elsewhere := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		followed = true
	}))
	defer elsewhere.Close()
	gw := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, elsewhere.URL+r.URL.Path, http.StatusTemporaryRedirect)
	}))
	defer gw.Close()
	c := &Client{config: &ClientConfig{AppName: "app", APIKey: "ak_secret:app", GatewayURL: gw.URL}, connected: true}
	_, err := newGatewayDatabaseClient(c).Query(context.Background(), "SELECT 1")
	if err == nil || !strings.Contains(err.Error(), "redirected") {
		t.Fatalf("err %v, want the redirect refused", err)
	}
	if followed {
		t.Fatal("the request, with its key, followed the redirect")
	}
}
