package gateway

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/DeBrosOfficial/network/migrations"
	"github.com/DeBrosOfficial/network/pkg/client"
	"github.com/DeBrosOfficial/network/pkg/deployments"
	"github.com/DeBrosOfficial/network/pkg/gateway/ctxkeys"
	deploymentshandlers "github.com/DeBrosOfficial/network/pkg/gateway/handlers/deployments"
	"github.com/DeBrosOfficial/network/pkg/logging"
	"github.com/DeBrosOfficial/network/pkg/rqlite"
	_ "github.com/mattn/go-sqlite3"
	"go.uber.org/zap"
)

// downHomeDB is a registry that cannot say where the home node is.
type downHomeDB struct{ client.DatabaseClient }

func (downHomeDB) Query(context.Context, string, ...interface{}) (*client.QueryResult, error) {
	return nil, errors.New("no leader")
}

func homeRouteGateway(t *testing.T) *Gateway {
	t.Helper()
	logger, err := logging.NewColoredLogger(logging.ComponentGateway, false)
	if err != nil {
		t.Fatal(err)
	}
	return &Gateway{
		logger:     logger,
		nodePeerID: "this-node",
		client:     &fakeNetworkClient{db: downHomeDB{}},
	}
}

// The bug: when the home node could not be reached, a change to the deployment
// ran on whichever node took the request, outside the home node's lock and
// version stamps. It must be refused so the caller retries.
func TestRouteToHomeNode_aChangeIsRefusedWhenTheHomeNodeCannotBeReached(t *testing.T) {
	g := homeRouteGateway(t)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/v1/deployments/rollback?name=api", nil)

	handled := g.routeToHomeNode(w, r, &deployments.Deployment{Name: "api", HomeNodeID: "other-node"}, true)

	if !handled || w.Code != http.StatusServiceUnavailable {
		t.Fatalf("handled %v, status %d; a change must be answered 503, not run here", handled, w.Code)
	}
	if w.Header().Get("Retry-After") == "" {
		t.Error("a retryable refusal should say when to retry")
	}
}

func TestRouteToHomeNode_aReadStillRunsHereWhenTheHomeNodeCannotBeReached(t *testing.T) {
	g := homeRouteGateway(t)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/v1/deployments/logs?name=api", nil)

	if g.routeToHomeNode(w, r, &deployments.Deployment{Name: "api", HomeNodeID: "other-node"}, false) {
		t.Fatal("a read was refused instead of falling through to the handler")
	}
	if w.Body.Len() != 0 {
		t.Errorf("a fall-through wrote %q", w.Body)
	}
}

// The bug: the header that says "already forwarded, run it here" was believed
// whoever set it, so a tenant could run an update, a rollback or an environment
// change on any node, outside the home node's lock and version stamps.
func TestDropForgedProxyNode_onlyAPeerOnTheOverlayKeepsIt(t *testing.T) {
	for remote, wantKept := range map[string]bool{
		"203.0.113.9:4000": false, // the internet
		"127.0.0.1:4000":   false, // a process on this host, a tenant's included
		"10.0.0.2:4000":    true,  // a peer node on the WireGuard overlay
	} {
		var got string
		h := dropForgedProxyNode(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
			got = r.Header.Get(headerProxyNode)
		}))
		r := httptest.NewRequest(http.MethodPost, "/v1/deployments/rollback?name=api", nil)
		r.RemoteAddr = remote
		r.Header.Set(headerProxyNode, "any-node")
		h.ServeHTTP(httptest.NewRecorder(), r)
		if (got != "") != wantKept {
			t.Errorf("from %s: header %q, kept = %v, want %v", remote, got, got != "", wantKept)
		}
	}
}

func homeRoutedDeployment(t *testing.T) *Gateway {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	db.SetMaxOpenConns(1)
	if err := rqlite.ApplyEmbeddedMigrations(t.Context(), db, migrations.FS, zap.NewNop()); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO deployments (id, namespace, name, type, home_node_id, deployed_by)
		VALUES ('dep-1', 'acme', 'api', 'go-backend', 'other-node', 'test')`); err != nil {
		t.Fatal(err)
	}
	codec, err := deployments.NewEnvCodec("test-cluster-secret")
	if err != nil {
		t.Fatal(err)
	}
	g := homeRouteGateway(t)
	g.deploymentService = deploymentshandlers.NewDeploymentService(rqlite.NewClient(db), nil, nil, nil, zap.NewNop(), "", codec, nil)
	return g
}

// delete names the deployment by id as often as by name; a request that names
// it by id must be routed like one that names it by name.
func TestHomeNodeHandler_aRequestNamingTheDeploymentByIDIsRoutedToItsHomeNode(t *testing.T) {
	for _, query := range []string{"name=api", "id=dep-1"} {
		g := homeRoutedDeployment(t)
		ran := false
		h := g.withHomeNodeOnly(func(http.ResponseWriter, *http.Request) { ran = true })
		r := httptest.NewRequest(http.MethodPost, "/v1/deployments/rollback?"+query, nil)
		r = r.WithContext(context.WithValue(r.Context(), ctxkeys.NamespaceOverride, "acme"))
		w := httptest.NewRecorder()

		h(w, r)

		if ran || w.Code != http.StatusServiceUnavailable {
			t.Errorf("?%s: handler ran = %v, status %d; want the home node tried and 503", query, ran, w.Code)
		}
	}
}
