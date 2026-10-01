package deployments

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/migrations"
	"github.com/DeBrosOfficial/network/pkg/gateway/ctxkeys"
	"github.com/DeBrosOfficial/network/pkg/rqlite"
	"github.com/DeBrosOfficial/network/pkg/rqlite/rqlitetest"
	"go.uber.org/zap"
)

// The domain handlers named a verification_status column the table never had,
// so `domain list` answered 500 and `domain add` could not insert. These tests
// run the handlers over the real migration.

const testDomainNamespace = "ns-a"

func newDomainHandlerOnSchema(t *testing.T) (*DomainHandler, func(sql string, args ...any)) {
	t.Helper()
	ddl, err := migrations.FS.ReadFile("007_deployments.sql")
	if err != nil {
		t.Fatalf("read migration: %v", err)
	}
	client, db := rqlitetest.SQLite(t, `CREATE TABLE schema_migrations (version INTEGER PRIMARY KEY, applied_at TIMESTAMP)`, string(ddl))
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := db.Exec(sql, args...); err != nil {
			t.Fatalf("exec %q: %v", sql, err)
		}
	}
	exec(`INSERT INTO deployments (id, namespace, name, type, version, content_cid, build_cid, home_node_id, port, subdomain, environment, deployed_by) VALUES ('d1', 'ns-a', 'web', 'static', 1, '', '', '', 0, 'web-x', '', 'w')`)
	exec(`INSERT INTO deployments (id, namespace, name, type, version, content_cid, build_cid, home_node_id, port, subdomain, environment, deployed_by) VALUES ('d2', 'ns-b', 'other', 'static', 1, '', '', '', 0, 'other-x', '', 'w')`)
	svc := &DeploymentService{db: client, logger: zap.NewNop(), baseDomain: "example.test", envCodec: testEnvCodec()}
	return NewDomainHandler(svc, zap.NewNop()), exec
}

func domainRequest(method, target, body string) *http.Request {
	r := httptest.NewRequest(method, target, strings.NewReader(body))
	return r.WithContext(context.WithValue(r.Context(), ctxkeys.NamespaceOverride, testDomainNamespace))
}

func listedDomains(t *testing.T, h *DomainHandler, target string) []map[string]any {
	t.Helper()
	w := httptest.NewRecorder()
	h.HandleListDomains(w, domainRequest(http.MethodGet, target, ""))
	if w.Code != http.StatusOK {
		t.Fatalf("list status = %d, body %q", w.Code, w.Body.String())
	}
	var resp struct {
		Domains []map[string]any `json:"domains"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	return resp.Domains
}

func TestHandleListDomains_empty_namespace(t *testing.T) {
	h, _ := newDomainHandlerOnSchema(t)
	if got := listedDomains(t, h, "/v1/deployments/domains/list"); len(got) != 0 {
		t.Errorf("domains = %v, want none", got)
	}
}

func TestHandleListDomains_status_follows_verified_at_and_namespace(t *testing.T) {
	h, exec := newDomainHandlerOnSchema(t)
	exec(`INSERT INTO deployment_domains (id, deployment_id, namespace, domain) VALUES ('1', 'd1', 'ns-a', 'pending.example.org')`)
	exec(`INSERT INTO deployment_domains (id, deployment_id, namespace, domain, verified_at) VALUES ('2', 'd1', 'ns-a', 'done.example.org', CURRENT_TIMESTAMP)`)
	exec(`INSERT INTO deployment_domains (id, deployment_id, namespace, domain) VALUES ('3', 'd2', 'ns-b', 'foreign.example.org')`)

	status := map[string]string{}
	for _, d := range listedDomains(t, h, "/v1/deployments/domains/list") {
		status[d["domain"].(string)] = d["verification_status"].(string)
	}
	if len(status) != 2 || status["pending.example.org"] != "pending" || status["done.example.org"] != "verified" {
		t.Errorf("statuses = %v, want the two ns-a domains, pending and verified", status)
	}

	if got := listedDomains(t, h, "/v1/deployments/domains/list?deployment_name=web"); len(got) != 2 {
		t.Errorf("filtered by deployment: %d domains, want 2", len(got))
	}
}

func TestHandleAddDomain_then_list_shows_pending(t *testing.T) {
	h, _ := newDomainHandlerOnSchema(t)
	w := httptest.NewRecorder()
	h.HandleAddDomain(w, domainRequest(http.MethodPost, "/v1/deployments/domains/add",
		`{"deployment_name":"web","domain":"shop.example.org"}`))
	if w.Code != http.StatusCreated {
		t.Fatalf("add status = %d, body %q", w.Code, w.Body.String())
	}
	got := listedDomains(t, h, "/v1/deployments/domains/list")
	if len(got) != 1 || got[0]["domain"] != "shop.example.org" || got[0]["verification_status"] != "pending" {
		t.Errorf("domains = %v, want shop.example.org pending", got)
	}

	w = httptest.NewRecorder()
	h.HandleAddDomain(w, domainRequest(http.MethodPost, "/v1/deployments/domains/add",
		`{"deployment_name":"web","domain":"shop.example.org"}`))
	if w.Code != http.StatusConflict {
		t.Errorf("duplicate add status = %d, want 409", w.Code)
	}
}

func TestHandleListDomains_unknown_deployment(t *testing.T) {
	h, _ := newDomainHandlerOnSchema(t)
	w := httptest.NewRecorder()
	h.HandleListDomains(w, domainRequest(http.MethodGet, "/v1/deployments/domains/list?deployment_name=nope", ""))
	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", w.Code)
	}
}

// racingDomainClient inserts another namespace's row for the domain just
// before the add's insert runs: the add's duplicate check has already passed,
// as it would when two adds of one domain run at once.
type racingDomainClient struct {
	rqlite.Client
	race func()
}

func (c racingDomainClient) Exec(ctx context.Context, query string, args ...any) (sql.Result, error) {
	if strings.Contains(query, "INSERT INTO deployment_domains") {
		c.race()
	}
	return c.Client.Exec(ctx, query, args...)
}

func TestHandleAddDomain_aConcurrentAddIs409Not500(t *testing.T) {
	h, exec := newDomainHandlerOnSchema(t)
	h.service.db = racingDomainClient{Client: h.service.db, race: func() {
		exec(`INSERT INTO deployment_domains (id, deployment_id, namespace, domain, is_custom, verification_token, created_at, updated_at)
			VALUES ('r1', 'd2', 'ns-b', 'race.example.org', TRUE, 't', datetime('now'), datetime('now'))`)
	}}
	w := httptest.NewRecorder()
	h.HandleAddDomain(w, domainRequest(http.MethodPost, "/v1/deployments/domains/add",
		`{"deployment_name":"web","domain":"race.example.org"}`))
	if w.Code != http.StatusConflict {
		t.Fatalf("add racing another add: status %d %q, want 409", w.Code, w.Body.String())
	}
}
