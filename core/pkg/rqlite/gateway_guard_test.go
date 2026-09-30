package rqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/sqlguard"
)

// errReached is what the recording client answers with, so a request that got
// past the guard comes back 500 and one the guard refused comes back 403.
var errReached = errors.New("reached the database")

// recordingExec is the executor behind a select's query builder.
type recordingExec struct{ c *recordingClient }

func (e recordingExec) QueryContext(_ context.Context, q string, _ ...any) (*sql.Rows, error) {
	e.c.reached = append(e.c.reached, q)
	return nil, errReached
}

func (e recordingExec) ExecContext(_ context.Context, q string, _ ...any) (sql.Result, error) {
	e.c.reached = append(e.c.reached, q)
	return nil, errReached
}

// recordingClient notes every statement that reaches it and fails it. The guard
// stands in front of the client, so `reached` is what the database would have run.
type recordingClient struct {
	Client
	reached []string
}

func (c *recordingClient) Query(_ context.Context, _ any, q string, _ ...any) error {
	c.reached = append(c.reached, q)
	return errReached
}

func (c *recordingClient) Exec(_ context.Context, q string, _ ...any) (sql.Result, error) {
	c.reached = append(c.reached, q)
	return nil, errReached
}

func (c *recordingClient) FindBy(_ context.Context, _ any, table string, _ map[string]any, _ ...FindOption) error {
	c.reached = append(c.reached, "find "+table)
	return errReached
}

func (c *recordingClient) FindOneBy(_ context.Context, _ any, table string, _ map[string]any, _ ...FindOption) error {
	c.reached = append(c.reached, "find-one "+table)
	return errReached
}

func (c *recordingClient) CreateQueryBuilder(table string) *QueryBuilder {
	return newQueryBuilder(recordingExec{c}, table)
}

func (c *recordingClient) Batch(_ context.Context, ops []BatchOp) (*BatchResult, error) {
	for _, op := range ops {
		c.reached = append(c.reached, op.SQL)
	}
	return &BatchResult{Committed: true}, nil
}

func guardedGateway(guard SQLGuard) (*HTTPGateway, *recordingClient) {
	c := &recordingClient{}
	g := NewHTTPGateway(c, "/v1/rqlite")
	g.SQLGuard = guard
	return g, c
}

// post sends body to path on g's mux and returns the status and decoded JSON.
func post(t *testing.T, g *HTTPGateway, path, body string) (int, map[string]any) {
	t.Helper()
	mux := http.NewServeMux()
	g.RegisterRoutes(mux)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, path, strings.NewReader(body)))
	out := map[string]any{}
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec.Code, out
}

func wantRefused(t *testing.T, name string, status int, body map[string]any, c *recordingClient) {
	t.Helper()
	if status != http.StatusForbidden {
		t.Errorf("%s: status = %d (%v), want 403", name, status, body)
	}
	if body["code"] != CodeSQLNotAllowed {
		t.Errorf("%s: code = %v, want %s", name, body["code"], CodeSQLNotAllowed)
	}
	if msg, _ := body["error"].(string); msg == "" {
		t.Errorf("%s: the refusal carries no message", name)
	}
	if len(c.reached) != 0 {
		t.Errorf("%s: %v reached the database", name, c.reached)
	}
}

// The escalation: a namespace member holding db:write, sending raw SQL at the
// tables that decide who may do what.
func TestGuardedGateway_exec_and_query_refusePlatformTables(t *testing.T) {
	for _, sql := range []string{
		"INSERT INTO grants(principal_id, namespace_id, role) VALUES (1, 1, 'owner')",
		"UPDATE api_keys SET scopes = 'admin'",
		"INSERT INTO ipfs_content_ownership(cid, namespace, uploaded_by) VALUES ('Qm', 'me', 'me')",
		"DELETE FROM ipfs_cid_refs",
		"UPDATE deployments SET content_cid = 'Qm'",
		`INSERT INTO "grants" VALUES (1)`,
		"INSERT INTO [grants] VALUES (1)",
		"INSERT INTO main.grants VALUES (1)",
		"INSERT INTO messages VALUES (1); DELETE FROM messages",
		"SELECT 1 /* harmless */ ; UPDATE grants SET role = 'owner'",
		"PRAGMA writable_schema = ON",
	} {
		for _, path := range []string{"/v1/rqlite/exec", "/v1/rqlite/query"} {
			g, c := guardedGateway(sqlguard.Check)
			body, _ := json.Marshal(map[string]any{"sql": sql})
			status, out := post(t, g, path, string(body))
			wantRefused(t, path+" "+sql, status, out, c)
		}
	}
}

func TestGuardedGateway_transaction_refusesAProtectedOpAndRunsNone(t *testing.T) {
	g, c := guardedGateway(sqlguard.Check)
	status, out := post(t, g, "/v1/rqlite/transaction", `{"ops":[
		{"kind":"exec","sql":"INSERT INTO messages(body) VALUES ('x')"},
		{"kind":"exec","sql":"INSERT INTO grants(role) VALUES ('owner')"}]}`)
	wantRefused(t, "transaction", status, out, c)
	if msg, _ := out["error"].(string); !strings.Contains(msg, "op 1") {
		t.Errorf("the refusal does not say which op: %q", msg)
	}

	// A query op is a statement too.
	g, c = guardedGateway(sqlguard.Check)
	status, out = post(t, g, "/v1/rqlite/transaction", `{"ops":[{"kind":"query","sql":"SELECT * FROM api_keys"}]}`)
	wantRefused(t, "transaction query op", status, out, c)

	// The legacy statements form is converted to ops and guarded the same way.
	g, c = guardedGateway(sqlguard.Check)
	status, out = post(t, g, "/v1/rqlite/transaction", `{"statements":["INSERT INTO messages(body) VALUES ('x')","DELETE FROM grants"]}`)
	wantRefused(t, "legacy statements", status, out, c)
}

func TestGuardedGateway_findSelectAndSchemaHelpers_refusePlatformTables(t *testing.T) {
	for name, tc := range map[string]struct{ path, body string }{
		"find table":        {"/v1/rqlite/find", `{"table":"api_keys"}`},
		"find-one table":    {"/v1/rqlite/find-one", `{"table":"grants","criteria":{"id":1}}`},
		"find join":         {"/v1/rqlite/find", `{"table":"messages","joins":[{"kind":"LEFT","table":"grants","on":"1=1"}]}`},
		"find select":       {"/v1/rqlite/find", `{"table":"messages","select":["(SELECT key FROM api_keys)"]}`},
		"find order by":     {"/v1/rqlite/find", `{"table":"messages","order_by":["(SELECT 1 FROM grants)"]}`},
		"find criteria key": {"/v1/rqlite/find", `{"table":"messages","criteria":{"id IN (SELECT id FROM grants) OR id":1}}`},
		"select table":      {"/v1/rqlite/select", `{"table":"namespaces"}`},
		"select alias":      {"/v1/rqlite/select", `{"table":"messages","alias":"x JOIN grants g ON 1=1 --"}`},
		"select where":      {"/v1/rqlite/select", `{"table":"messages","where":[{"expr":"id IN (SELECT id FROM \"api_keys\")"}]}`},
		"select join":       {"/v1/rqlite/select", `{"table":"messages","joins":[{"table":"principals","on":"1=1"}]}`},
		"select group by":   {"/v1/rqlite/select", `{"table":"messages","group_by":["grants.id"]}`},
		"create-table":      {"/v1/rqlite/create-table", `{"schema":"CREATE TABLE grants (id INTEGER)"}`},
		"create-table 2":    {"/v1/rqlite/create-table", `{"schema":"CREATE TABLE ok (id INTEGER); CREATE TABLE x (id INTEGER)"}`},
		"drop-table":        {"/v1/rqlite/drop-table", `{"table":"grants"}`},
	} {
		g, c := guardedGateway(sqlguard.Check)
		status, out := post(t, g, tc.path, tc.body)
		wantRefused(t, name, status, out, c)
	}
}

// The other half: a tenant's own tables are untouched by the guard.
func TestGuardedGateway_tenantSQLOnItsOwnTablesStillRuns(t *testing.T) {
	for name, tc := range map[string]struct{ path, body, reached string }{
		"exec":         {"/v1/rqlite/exec", `{"sql":"INSERT INTO messages(body) VALUES (?)","args":["grants"]}`, "INSERT INTO messages"},
		"query":        {"/v1/rqlite/query", `{"sql":"SELECT * FROM messages WHERE body = ?","args":["api_keys"]}`, "SELECT * FROM messages"},
		"trailing ;":   {"/v1/rqlite/exec", `{"sql":"DELETE FROM messages;"}`, "DELETE FROM messages"},
		"comment name": {"/v1/rqlite/exec", `{"sql":"DELETE FROM messages -- not grants\n"}`, "DELETE FROM messages"},
		"quoted own":   {"/v1/rqlite/exec", `{"sql":"DELETE FROM \"my table\""}`, "DELETE FROM"},
		"find":         {"/v1/rqlite/find", `{"table":"messages","criteria":{"room":"a"},"limit":5}`, "find messages"},
		"find-one":     {"/v1/rqlite/find-one", `{"table":"messages","criteria":{"messages.room":"a"}}`, "find-one messages"},
		"select":       {"/v1/rqlite/select", `{"table":"messages","alias":"m","where":[{"expr":"m.room = ?","args":["a"]}],"order_by":["m.id DESC"]}`, "SELECT * FROM messages AS m"},
		"create-table": {"/v1/rqlite/create-table", `{"schema":"CREATE TABLE notes (id INTEGER PRIMARY KEY, body TEXT)"}`, "CREATE TABLE notes"},
		"drop-table":   {"/v1/rqlite/drop-table", `{"table":"notes"}`, "DROP TABLE notes"},
	} {
		g, c := guardedGateway(sqlguard.Check)
		status, out := post(t, g, tc.path, tc.body)
		if status == http.StatusForbidden {
			t.Errorf("%s: refused: %v", name, out)
			continue
		}
		if len(c.reached) != 1 || !strings.HasPrefix(c.reached[0], tc.reached) {
			t.Errorf("%s: reached = %v, want one statement starting %q", name, c.reached, tc.reached)
		}
	}

	g, c := guardedGateway(sqlguard.Check)
	status, out := post(t, g, "/v1/rqlite/transaction", `{"ops":[
		{"kind":"exec","sql":"INSERT INTO messages(body) VALUES ('x')"},
		{"kind":"query","sql":"SELECT COUNT(*) FROM messages"}]}`)
	if status != http.StatusOK || len(c.reached) != 2 {
		t.Errorf("transaction on the tenant's own table: status %d (%v), reached %v", status, out, c.reached)
	}
}

// The cluster gateway installs no guard: its raw-database routes are an
// operator's, and the registry is what they are for.
func TestUnguardedGateway_operatorSQLOnPlatformTablesRuns(t *testing.T) {
	g, c := guardedGateway(nil)
	status, _ := post(t, g, "/v1/rqlite/exec", `{"sql":"UPDATE grants SET role = 'owner'"}`)
	if status == http.StatusForbidden || len(c.reached) != 1 {
		t.Errorf("status %d, reached %v; an unguarded gateway must pass the statement through", status, c.reached)
	}
	status, _ = post(t, g, "/v1/rqlite/find", `{"table":"api_keys","criteria":{"id":1}}`)
	if status == http.StatusForbidden || len(c.reached) != 2 {
		t.Errorf("find on an unguarded gateway: status %d, reached %v", status, c.reached)
	}
}

func TestGuardedGateway_findRefusesACriteriaKeyThatIsNotAColumn(t *testing.T) {
	for _, key := range []string{"a = 1 OR a", "a'--", "a;b", "", "a.b.c", "1a"} {
		g, c := guardedGateway(sqlguard.Check)
		body, _ := json.Marshal(map[string]any{"table": "messages", "criteria": map[string]any{key: 1}})
		status, out := post(t, g, "/v1/rqlite/find", string(body))
		wantRefused(t, "criteria key "+key, status, out, c)
	}
}

// Whatever the guard says, it says it before the database is asked, and its
// wording reaches the caller.
func TestGuardedGateway_refusalCarriesTheGuardsReason(t *testing.T) {
	g, c := guardedGateway(func(string) error { return errors.New("no thanks") })
	status, out := post(t, g, "/v1/rqlite/query", `{"sql":"SELECT 1"}`)
	wantRefused(t, "custom guard", status, out, c)
	if out["error"] != "no thanks" {
		t.Errorf("error = %v", out["error"])
	}
}
