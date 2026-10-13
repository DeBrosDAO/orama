package gateway

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/gateway/auth"
	"github.com/DeBrosOfficial/network/pkg/rqlite"
)

// ormRecorder notes every statement the ORM gateway hands the database.
type ormRecorder struct {
	rqlite.Client
	ran []string
}

func (o *ormRecorder) Exec(_ context.Context, q string, _ ...any) (sql.Result, error) {
	o.ran = append(o.ran, q)
	return driverResult{}, nil
}

func (o *ormRecorder) Batch(_ context.Context, ops []rqlite.BatchOp) (*rqlite.BatchResult, error) {
	for _, op := range ops {
		o.ran = append(o.ran, op.SQL)
	}
	return &rqlite.BatchResult{Committed: true}, nil
}

// CreateQueryBuilder builds real statements and never runs them: a select the
// guard lets through fails on the missing database, which is not a 403.
func (o *ormRecorder) CreateQueryBuilder(table string) *rqlite.QueryBuilder {
	return rqlite.NewClient(nil).CreateQueryBuilder(table)
}

type driverResult struct{}

func (driverResult) LastInsertId() (int64, error) { return 0, nil }
func (driverResult) RowsAffected() (int64, error) { return 1, nil }

// ormRoutes mounts the ORM gateway over a recording database, configured the
// way Routes configures it, for a gateway that serves servesNamespace. It skips
// the middleware chain: the guard is a separate gate from who may reach the route.
func ormRoutes(t *testing.T, servesNamespace string) (http.Handler, *ormRecorder) {
	t.Helper()
	g := chainGateway(t, servesNamespace, &stubKeyDatabase{})
	db := &ormRecorder{}
	g.ormHTTP = rqlite.NewHTTPGateway(db, "")
	g.configureORMGateway()
	mux := http.NewServeMux()
	g.ormHTTP.RegisterRoutes(mux)
	return mux, db
}

func postORM(h http.Handler, path, body string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, path, strings.NewReader(body)))
	return rec
}

// The route policy is what a developer passes: db:write. The guard is a second,
// independent gate, and it does not ask who the caller is — the developer,
// admin and owner of a namespace all reach the same database, and none of them
// may write the tables that decide who may do what in it.
func TestORMRoutes_namespaceGatewayRefusesPlatformTablesToEveryRole(t *testing.T) {
	if p := policyOf(http.MethodPost, "/v1/rqlite/exec"); p.Domain != string(auth.DomainDB) || p.Action != string(auth.ActionWrite) {
		t.Fatalf("/v1/rqlite/exec requires %s:%s, not db:write; this test's premise is stale", p.Domain, p.Action)
	}

	h, db := ormRoutes(t, "acme")
	for path, body := range map[string]string{
		"/v1/rqlite/exec":        `{"sql":"UPDATE grants SET role = 'owner'"}`,
		"/v1/rqlite/query":       `{"sql":"SELECT key FROM api_keys"}`,
		"/v1/rqlite/transaction": `{"ops":[{"kind":"exec","sql":"INSERT INTO t VALUES (1)"},{"kind":"exec","sql":"INSERT INTO ipfs_content_ownership(cid, namespace) VALUES ('Qm', 'acme')"}]}`,
		"/v1/rqlite/select":      `{"table":"grants"}`,
		"/v1/rqlite/find":        `{"table":"ipfs_cid_refs"}`,
	} {
		rec := postORM(h, path, body)
		if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), rqlite.CodeSQLNotAllowed) {
			t.Errorf("%s: status %d body %s, want 403 %s", path, rec.Code, rec.Body.String(), rqlite.CodeSQLNotAllowed)
		}
	}
	if len(db.ran) != 0 {
		t.Errorf("the database ran %v", db.ran)
	}
}

func TestORMRoutes_namespaceGatewayStillRunsTheTenantsOwnSQL(t *testing.T) {
	h, db := ormRoutes(t, "acme")
	if rec := postORM(h, "/v1/rqlite/exec", `{"sql":"INSERT INTO messages(body) VALUES (?)","args":["grants"]}`); rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	if rec := postORM(h, "/v1/rqlite/transaction", `{"ops":[{"kind":"exec","sql":"DELETE FROM messages"}]}`); rec.Code != http.StatusOK {
		t.Fatalf("transaction: status %d: %s", rec.Code, rec.Body.String())
	}
	if len(db.ran) != 2 {
		t.Errorf("ran %v, want both statements", db.ran)
	}
}

// The cluster gateway's raw-database routes are the operator's, and the
// operator's job there is the registry. requireOperatorForCoreRegistry gates who
// arrives; the guard must not undo what it lets through.
func TestORMRoutes_clusterGatewayLeavesTheRegistryToItsOperators(t *testing.T) {
	for _, served := range []string{"index", "default", ""} {
		h, db := ormRoutes(t, served)
		rec := postORM(h, "/v1/rqlite/exec", `{"sql":"UPDATE grants SET role = 'admin' WHERE id = 1"}`)
		if rec.Code != http.StatusOK || len(db.ran) != 1 {
			t.Errorf("gateway serving %q: status %d, ran %v; an operator's statement on the registry was refused", served, rec.Code, db.ran)
		}
	}
}

func TestORMSQLGuard_isKeyedOnTheDatabaseTheGatewayServes(t *testing.T) {
	logger := chainGateway(t, "acme", &stubKeyDatabase{}).logger
	for _, tc := range []struct {
		name    string
		g       *Gateway
		guarded bool
	}{
		{"namespace gateway", &Gateway{logger: logger, cfg: &Config{ClientNamespace: "acme"}}, true},
		{"index gateway", &Gateway{logger: logger, cfg: &Config{ClientNamespace: "index"}}, false},
		{"default gateway", &Gateway{logger: logger, cfg: &Config{ClientNamespace: "default"}}, false},
		// A gateway with no configuration is not known to serve the registry,
		// so it is refused rather than trusted.
		{"no configuration", &Gateway{logger: logger}, true},
	} {
		if got := tc.g.ormSQLGuard() != nil; got != tc.guarded {
			t.Errorf("%s: guarded = %v, want %v", tc.name, got, tc.guarded)
		}
	}
}
