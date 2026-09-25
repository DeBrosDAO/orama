package gateway

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/pkg/client"
	"github.com/DeBrosOfficial/network/pkg/logging"
)

const hopNamespace = "anchat"

// countingGrantRegistry is the cluster registry as a namespace gateway sees it
// through its registry client: it answers the grant lookup and counts queries.
type countingGrantRegistry struct {
	*grantRegistry
	queries int
}

func (c *countingGrantRegistry) Database() client.DatabaseClient { return c }

func (c *countingGrantRegistry) Query(ctx context.Context, query string, args ...interface{}) (*client.QueryResult, error) {
	c.queries++
	return c.grantRegistry.Query(ctx, query, args...)
}

// tenantDB is the namespace's own rqlite, which holds no grants. A grant
// lookup that reaches it is the bug.
type tenantDB struct {
	client.DatabaseClient
	t *testing.T
}

func (d *tenantDB) Query(_ context.Context, query string, _ ...interface{}) (*client.QueryResult, error) {
	d.t.Errorf("the tenant's own database was asked: %s", query)
	return &client.QueryResult{}, nil
}

// namespaceGatewayForHops is a namespace gateway whose registry client
// answers grants with role.
func namespaceGatewayForHops(t *testing.T, role string) (*Gateway, *countingGrantRegistry) {
	t.Helper()
	logger, _ := logging.NewColoredLogger(logging.ComponentGateway, false)
	registry := &countingGrantRegistry{grantRegistry: &grantRegistry{role: role}}
	return &Gateway{
		logger:          logger,
		internalAuthKey: testHopKey(t),
		authService:     newAuthServiceForTest(t),
		authClient:      registry,
		client:          &fakeNetworkClient{db: &tenantDB{t: t}},
		cfg:             &Config{ClientNamespace: hopNamespace},
	}, registry
}

// hop is a request as the main gateway forwards it: signed internal-auth
// headers naming the namespace and the caller's JWT subject.
func hop(t *testing.T, g *Gateway, method, path, namespace, sub string) *http.Request {
	t.Helper()
	r := httptest.NewRequest(method, path, nil)
	r.Header.Set(HeaderInternalAuthValidated, "true")
	r.Header.Set(HeaderInternalAuthNamespace, namespace)
	r.Header.Set(HeaderInternalAuthJWTSub, sub)
	if err := signInternalAuthHeaders(g.internalAuthKey, r.Header, method, path, time.Now()); err != nil {
		t.Fatalf("sign: %v", err)
	}
	return r
}

// serveHop runs the namespace gateway's real chain.
func serveHop(g *Gateway, r *http.Request) (*httptest.ResponseRecorder, bool) {
	reached := false
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		reached = true
		w.WriteHeader(http.StatusOK)
	})
	rec := httptest.NewRecorder()
	g.internalAuthMiddleware(g.routePolicyMiddleware(g.authMiddleware(
		g.authorizationMiddleware(g.scopeMiddleware(next))))).ServeHTTP(rec, r)
	return rec, reached
}

const hopWallet = "0x1111111111111111111111111111111111111111"

// The hop carries identity and not the grant, and the namespace gateway did not
// look the grant up, so a signed-in owner managing functions through the main
// gateway was refused INSUFFICIENT_SCOPE. With bugboard #427 routing every
// tenant's function management there, that was every JWT caller.
func TestAuthorizationMiddleware_forwardedCallerGetsItsGrantOnTheControlPlane(t *testing.T) {
	for _, sub := range []string{hopWallet, "ak_exchanged_key"} {
		g, registry := namespaceGatewayForHops(t, "owner")

		rec, reached := serveHop(g, hop(t, g, http.MethodPost, "/v1/functions", hopNamespace, sub))

		if !reached {
			t.Errorf("%s: an owner's forwarded deploy was refused %d: %s", sub, rec.Code, rec.Body.String())
		}
		if registry.queries == 0 {
			t.Errorf("%s: the grant was not read from the registry", sub)
		}
	}
}

func TestAuthorizationMiddleware_forwardedCallerWithoutAGrantIsRefused(t *testing.T) {
	g, _ := namespaceGatewayForHops(t, "")

	rec, reached := serveHop(g, hop(t, g, http.MethodPost, "/v1/functions", hopNamespace, hopWallet))

	if reached || rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), CodeOwnershipRequired) {
		t.Errorf("reached %v, status %d, body %s; want 403 %s", reached, rec.Code, rec.Body.String(), CodeOwnershipRequired)
	}
}

// The data plane a wallet reaches without a grant stays free of registry
// round trips: that is the path every publish takes.
func TestAuthorizationMiddleware_forwardedDataPlaneSkipsTheGrantLookup(t *testing.T) {
	g, registry := namespaceGatewayForHops(t, "owner")

	rec, reached := serveHop(g, hop(t, g, http.MethodPost, "/v1/pubsub/publish", hopNamespace, hopWallet))

	if !reached {
		t.Fatalf("a forwarded publish was refused %d: %s", rec.Code, rec.Body.String())
	}
	if registry.queries != 0 {
		t.Errorf("a forwarded publish made %d registry queries", registry.queries)
	}
}

// A namespace gateway serves one namespace. A genuine hop naming another —
// a stale target sending one tenant's traffic to another's gateway — is
// refused rather than served as the other tenant.
func TestAuthMiddleware_hopForAnotherNamespaceIsRefused(t *testing.T) {
	g, _ := namespaceGatewayForHops(t, "owner")

	rec, reached := serveHop(g, hop(t, g, http.MethodPost, "/v1/pubsub/publish", "tenant-b", hopWallet))

	if reached || rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), CodeNamespaceMismatch) {
		t.Errorf("reached %v, status %d, body %s; want 403 %s", reached, rec.Code, rec.Body.String(), CodeNamespaceMismatch)
	}
}

func TestFunctionDatabaseNamespace_theRegistryIsNoFunctionsDatabase(t *testing.T) {
	for clientNamespace, want := range map[string]string{
		"default":   "",
		"":          "",
		" anchat ":  "anchat",
		"tenant-a":  "tenant-a",
		" default ": "",
	} {
		if got := functionDatabaseNamespace(&Config{ClientNamespace: clientNamespace}); got != want {
			t.Errorf("client_namespace %q: function database namespace %q, want %q", clientNamespace, got, want)
		}
	}
}
