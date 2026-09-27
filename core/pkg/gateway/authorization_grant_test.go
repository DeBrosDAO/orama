package gateway

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/client"
	"github.com/DeBrosOfficial/network/pkg/gateway/auth"
	"github.com/DeBrosOfficial/network/pkg/gateway/ctxkeys"
	"github.com/DeBrosOfficial/network/pkg/logging"
)

// The authorization middleware is what decides whether a caller belongs to a
// namespace, and what role they hold there. Both halves matter and neither is
// testable by calling something else: a test of the grant lookup alone would
// not notice the middleware forgetting to consult it, and a test of the scope
// gate alone would not notice the role never being put in the context.

// grantRegistry answers the questions the authorization middleware asks.
type grantRegistry struct {
	client.DatabaseClient
	client.NetworkClient

	// role is what the namespace's grant lookup returns; "" means the caller
	// holds no grant at all.
	role string
}

func (g *grantRegistry) Database() client.DatabaseClient { return g }

func (g *grantRegistry) Query(_ context.Context, query string, _ ...interface{}) (*client.QueryResult, error) {
	switch {
	case strings.Contains(query, "INSERT OR IGNORE INTO namespaces"):
		return &client.QueryResult{Count: 1}, nil
	case strings.Contains(query, "SELECT id FROM namespaces"):
		return &client.QueryResult{Count: 1, Rows: [][]interface{}{{int64(1)}}}, nil
	case strings.Contains(query, "SELECT g.role, g.resource"):
		if g.role == "" {
			return &client.QueryResult{}, nil
		}
		return &client.QueryResult{Count: 1, Rows: [][]interface{}{
			{g.role, "", "", "", "", ""},
		}}, nil
	}
	return &client.QueryResult{}, nil
}

func grantGateway(t *testing.T, role string) *Gateway {
	t.Helper()
	logger, _ := logging.NewColoredLogger(logging.ComponentGateway, false)
	registry := &grantRegistry{role: role}
	svc, err := auth.NewService(logger, registry, "", "default")
	if err != nil {
		t.Fatalf("auth service: %v", err)
	}
	return &Gateway{
		logger:      logger,
		client:      registry,
		authService: svc,
		cfg:         &Config{ClientNamespace: "anchat", BaseDomain: "dbrs.space"},
	}
}

func grantRequest(wallet string) *http.Request {
	r := httptest.NewRequest(http.MethodPost, "/v1/pubsub/publish", nil)
	ctx := context.WithValue(r.Context(), ctxkeys.JWT, &auth.JWTClaims{Sub: wallet})
	ctx = context.WithValue(ctx, ctxkeys.NamespaceOverride, "anchat")
	return r.WithContext(ctx)
}

// A caller holding no grant in the namespace is refused. The gate used to ask
// "is there an ownership row", which is the same question with one answer.
func TestAuthorizationMiddleware_refusesACallerWithNoGrant(t *testing.T) {
	g := grantGateway(t, "")

	var reached bool
	chain := g.authorizationMiddleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		reached = true
	}))

	w := httptest.NewRecorder()
	chain.ServeHTTP(w, grantRequest("0xstranger"))

	if reached {
		t.Fatal("a wallet with no grant reached the handler")
	}
	if w.Code != http.StatusForbidden {
		t.Errorf("status %d, want 403: %s", w.Code, strings.TrimSpace(w.Body.String()))
	}
	if !strings.Contains(w.Body.String(), CodeOwnershipRequired) {
		t.Errorf("the refusal carries no code: %s", strings.TrimSpace(w.Body.String()))
	}
}

// The role has to reach the scope gate, or every member is an admin again —
// which is what the boolean this replaced could not avoid.
func TestAuthorizationMiddleware_putsTheRoleInTheContext(t *testing.T) {
	for role, wantAdmin := range map[string]bool{
		string(auth.RoleOwner):   true,
		string(auth.RoleAdmin):   true,
		string(auth.RoleRuntime): false,
		string(auth.RoleReader):  false,
	} {
		t.Run(role, func(t *testing.T) {
			g := grantGateway(t, role)

			var perms auth.PermissionSet
			var reached bool
			chain := g.authorizationMiddleware(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
				reached = true
				perms = g.callerPermissions(r)
			}))

			w := httptest.NewRecorder()
			chain.ServeHTTP(w, grantRequest("0xmember"))

			if !reached {
				t.Fatalf("a %s was refused: %d %s", role, w.Code, strings.TrimSpace(w.Body.String()))
			}
			if got := perms.IsAdmin(); got != wantAdmin {
				t.Errorf("a %s resolves to admin=%v, want %v", role, got, wantAdmin)
			}
		})
	}
}

// controlChain is the authorization gate and the scope gate, which is the
// chain that refused an owner on namespace list: the route does not require
// ownership, so the grant was never put on the request, and the scope gate
// then saw only the data plane.
func controlChain(g *Gateway) (http.Handler, *bool) {
	reached := false
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		reached = true
		w.WriteHeader(http.StatusOK)
	})
	return g.authorizationMiddleware(g.scopeMiddleware(next)), &reached
}

func grantWalletRequest(method, path, wallet, namespace string) *http.Request {
	r := httptest.NewRequest(method, path, nil)
	ctx := context.WithValue(r.Context(), ctxkeys.JWT, &auth.JWTClaims{Sub: wallet})
	ctx = context.WithValue(ctx, ctxkeys.NamespaceOverride, namespace)
	return r.WithContext(ctx)
}

func TestAuthorizationMiddleware_ownerReachesAControlRouteThatDoesNotRequireOwnership(t *testing.T) {
	g, registry := controlPlaneGateway(t, string(auth.RoleOwner))
	chain, reached := controlChain(g)

	w := httptest.NewRecorder()
	chain.ServeHTTP(w, grantWalletRequest(http.MethodGet, "/v1/namespace/list", "0xowner", "anchat"))

	if !*reached {
		t.Fatalf("an owner was refused namespace list: %d %s", w.Code, strings.TrimSpace(w.Body.String()))
	}
	if registry.queries == 0 {
		t.Fatal("the owner's grant was not read")
	}
}

func TestAuthorizationMiddleware_aWalletWithNoGrantIsRefusedTheControlPlane(t *testing.T) {
	g, _ := controlPlaneGateway(t, "")
	chain, reached := controlChain(g)

	w := httptest.NewRecorder()
	chain.ServeHTTP(w, grantWalletRequest(http.MethodGet, "/v1/namespace/list", "0xstranger", "anchat"))

	if *reached || w.Code != http.StatusForbidden || !strings.Contains(w.Body.String(), CodeScopeMissing) {
		t.Fatalf("reached %v, status %d, body %s; want 403 %s", *reached, w.Code, strings.TrimSpace(w.Body.String()), CodeScopeMissing)
	}
}

func TestAuthorizationMiddleware_developerReachesDeployAndNotTheNamespace(t *testing.T) {
	g, _ := controlPlaneGateway(t, string(auth.RoleDeveloper))
	chain, reached := controlChain(g)

	w := httptest.NewRecorder()
	chain.ServeHTTP(w, grantWalletRequest(http.MethodGet, "/v1/deployments/list", "0xdev", "anchat"))
	if !*reached {
		t.Fatalf("a developer was refused deployments: %d %s", w.Code, strings.TrimSpace(w.Body.String()))
	}

	chain, reached = controlChain(g)
	w = httptest.NewRecorder()
	chain.ServeHTTP(w, grantWalletRequest(http.MethodGet, "/v1/namespace/list", "0xdev", "anchat"))
	if *reached || w.Code != http.StatusForbidden {
		t.Fatalf("a developer listed namespaces: reached %v status %d %s", *reached, w.Code, strings.TrimSpace(w.Body.String()))
	}
}

// A reader grant is empty. Resolving it on cache would take the data plane
// away from a member the route never asked to own. The lookup stays off this
// path, including for an owner: a selector on a storage grant is not applied
// by a route that does not resolve one.
func TestAuthorizationMiddleware_dataPlaneDoesNotResolveAGrant(t *testing.T) {
	for _, role := range []string{string(auth.RoleOwner), string(auth.RoleReader)} {
		t.Run(role, func(t *testing.T) {
			g, registry := controlPlaneGateway(t, role)
			chain, reached := controlChain(g)

			w := httptest.NewRecorder()
			chain.ServeHTTP(w, grantWalletRequest(http.MethodPost, "/v1/cache/get", "0xmember", "anchat"))

			if !*reached {
				t.Fatalf("cache refused a %s: %d %s", role, w.Code, strings.TrimSpace(w.Body.String()))
			}
			if registry.queries != 0 {
				t.Errorf("cache made %d registry queries", registry.queries)
			}
		})
	}
}

func controlPlaneGateway(t *testing.T, role string) (*Gateway, *countingGrantRegistry) {
	t.Helper()
	logger, _ := logging.NewColoredLogger(logging.ComponentGateway, false)
	registry := &countingGrantRegistry{grantRegistry: &grantRegistry{role: role}}
	svc, err := auth.NewService(logger, registry, "", "anchat")
	if err != nil {
		t.Fatalf("auth service: %v", err)
	}
	return &Gateway{
		logger:      logger,
		client:      registry,
		authService: svc,
		cfg:         &Config{ClientNamespace: "anchat", BaseDomain: "dbrs.space"},
	}, registry
}
