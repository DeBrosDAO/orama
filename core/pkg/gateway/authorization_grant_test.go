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
