package gateway

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/pkg/client"
	gwauth "github.com/DeBrosOfficial/network/pkg/gateway/auth"
	"github.com/DeBrosOfficial/network/pkg/logging"
)

// The middleware chain is where who-may-call-what is actually decided, and
// until now no test ran it. The scope policy had unit tests for the pure
// requiredScope function, cross-namespace isolation was covered only in
// `//go:build e2e` suites `make test` does not run, and scopeMiddleware and
// authorizationMiddleware were never invoked at all.
//
// These run the real chain — authMiddleware, authorizationMiddleware,
// scopeMiddleware — for each kind of caller, against real routes.

// stubKeyDatabase answers the API-key lookup with a fixed row, so the chain can
// resolve a key without a registry behind it.
type stubKeyDatabase struct {
	client.DatabaseClient
	namespace string
	scopes    string
	found     bool
}

func (s *stubKeyDatabase) Query(_ context.Context, _ string, _ ...interface{}) (*client.QueryResult, error) {
	if !s.found {
		return &client.QueryResult{Columns: []string{"namespaces.name", "api_keys.scopes"}, Count: 0}, nil
	}
	return &client.QueryResult{
		Columns: []string{"namespaces.name", "api_keys.scopes"},
		Rows:    [][]interface{}{{s.namespace, s.scopes}},
		Count:   1,
	}, nil
}

// chainGateway builds a gateway serving one namespace, whose key lookups answer
// with the given key's namespace and grants.
func chainGateway(t *testing.T, servesNamespace string, key *stubKeyDatabase) *Gateway {
	t.Helper()
	logger, err := logging.NewColoredLogger(logging.ComponentGateway, false)
	if err != nil {
		t.Fatalf("logger: %v", err)
	}
	svc, err := gwauth.NewService(logger, nil, "", servesNamespace)
	if err != nil {
		t.Fatalf("auth service: %v", err)
	}
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("signing key: %v", err)
	}
	svc.SetEdDSAKey(priv)
	return &Gateway{
		logger:      logger,
		authService: svc,
		client:      &fakeNetworkClient{db: key},
		cfg:         &Config{ClientNamespace: servesNamespace},
	}
}

// serve runs the whole chain and reports what the caller got, and whether the
// handler was reached at all.
func serve(g *Gateway, req *http.Request) (status int, body string, reached bool) {
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		reached = true
		w.WriteHeader(http.StatusOK)
	})
	chain := g.authMiddleware(g.authorizationMiddleware(g.scopeMiddleware(next)))
	rec := httptest.NewRecorder()
	chain.ServeHTTP(rec, req)
	return rec.Code, rec.Body.String(), reached
}

func chainRequest(method, path string, header map[string]string) *http.Request {
	req := httptest.NewRequest(method, path, nil)
	for k, v := range header {
		req.Header.Set(k, v)
	}
	return req
}

func TestChain_anonymous(t *testing.T) {
	g := chainGateway(t, "alice", &stubKeyDatabase{})

	if status, _, reached := serve(g, chainRequest(http.MethodGet, "/v1/health", nil)); !reached || status != http.StatusOK {
		t.Errorf("a public route refused an anonymous caller: status=%d reached=%v", status, reached)
	}

	status, body, reached := serve(g, chainRequest(http.MethodGet, "/v1/cache/get", nil))
	if reached {
		t.Error("an anonymous caller reached a route that needs a credential")
	}
	if status != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401 (%s)", status, strings.TrimSpace(body))
	}
}

// An invite token in an Authorization header is not an API key, and enrolling
// a node is the route that proved it: the middleware took the Bearer token as
// an API key, found nothing, and answered 401 before the handler could check
// the token it actually was.
func TestChain_enrollingANodeReachesItsHandler(t *testing.T) {
	g := chainGateway(t, "index", &stubKeyDatabase{})

	status, body, reached := serve(g, chainRequest(http.MethodPost, "/v1/node/enroll", map[string]string{
		"Authorization": "Bearer an-invite-token",
	}))
	if !reached {
		t.Fatalf("the enroll handler was not reached: status=%d body=%s", status, strings.TrimSpace(body))
	}
}

func TestChain_aRuntimeKeyGetsItsOwnGrantsAndNoMore(t *testing.T) {
	g := chainGateway(t, "alice", &stubKeyDatabase{namespace: "alice", scopes: "invoke,cache", found: true})
	withKey := map[string]string{"X-API-Key": "ak_runtime:alice"}

	if status, body, reached := serve(g, chainRequest(http.MethodPost, "/v1/cache/get", withKey)); !reached {
		t.Errorf("a cache-scoped key was refused /v1/cache/get: status=%d body=%s", status, strings.TrimSpace(body))
	}

	status, body, reached := serve(g, chainRequest(http.MethodGet, "/v1/deployments/list", withKey))
	if reached {
		t.Error("a runtime key reached an admin route")
	}
	if status != http.StatusForbidden {
		t.Errorf("status = %d, want 403", status)
	}
	if !strings.Contains(body, "INSUFFICIENT_SCOPE") {
		t.Errorf("the refusal does not name the missing grant: %s", strings.TrimSpace(body))
	}
	if !strings.Contains(body, "admin") {
		t.Errorf("the refusal does not say which grant is required: %s", strings.TrimSpace(body))
	}
}

// storage, webrtc and proxy need a logged-in user on top of the grant, so a
// bare API key holding `storage` still cannot upload. That is deliberate and
// easy to lose: the grant check passes and a second one refuses.
func TestChain_aBareKeyWithStorageStillNeedsAUser(t *testing.T) {
	g := chainGateway(t, "alice", &stubKeyDatabase{namespace: "alice", scopes: "storage", found: true})

	status, body, reached := serve(g, chainRequest(http.MethodPost, "/v1/storage/upload",
		map[string]string{"X-API-Key": "ak_runtime:alice"}))
	if reached {
		t.Fatal("a bare API key uploaded to storage")
	}
	if status != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", status)
	}
	if !strings.Contains(body, "USER_JWT_REQUIRED") {
		t.Errorf("the refusal does not say a user is required: %s", strings.TrimSpace(body))
	}
}

func TestChain_anAdminKeyReachesTheControlPlane(t *testing.T) {
	g := chainGateway(t, "alice", &stubKeyDatabase{namespace: "alice", scopes: "admin", found: true})

	if status, body, reached := serve(g, chainRequest(http.MethodGet, "/v1/deployments/list",
		map[string]string{"X-API-Key": "ak_admin:alice"})); !reached {
		t.Errorf("an admin key was refused: status=%d body=%s", status, strings.TrimSpace(body))
	}
}

// The isolation property, which until now was tested only in an e2e suite
// `make test` does not run: alice's gateway does not serve bob's key.
func TestChain_aKeyFromAnotherNamespaceIsRefused(t *testing.T) {
	g := chainGateway(t, "alice", &stubKeyDatabase{namespace: "bob", scopes: "admin", found: true})

	status, body, reached := serve(g, chainRequest(http.MethodPost, "/v1/storage/upload",
		map[string]string{"X-API-Key": "ak_bob:bob"}))
	if reached {
		t.Fatal("bob's key reached alice's gateway")
	}
	if status != http.StatusForbidden {
		t.Errorf("status = %d, want 403", status)
	}
	if !strings.Contains(body, CodeNamespaceMismatch) {
		t.Errorf("the refusal carries no machine-readable code: %s", strings.TrimSpace(body))
	}
	if !strings.Contains(body, "belongs to another namespace") {
		t.Errorf("the refusal does not say why: %s", strings.TrimSpace(body))
	}
}

// A key the registry does not know is not a caller. The empty-result path is
// distinct from the error path and is the one a revoked key takes.
func TestChain_anUnknownKeyIsRefused(t *testing.T) {
	g := chainGateway(t, "alice", &stubKeyDatabase{found: false})

	status, _, reached := serve(g, chainRequest(http.MethodPost, "/v1/storage/upload",
		map[string]string{"X-API-Key": "ak_revoked:alice"}))
	if reached {
		t.Fatal("a key the registry does not know reached the handler")
	}
	if status != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", status)
	}
}

// A signed-in wallet holds the data-plane grants and never admin, so it reaches
// its own namespace's data and not the control plane.
func TestChain_aWalletJWTIsDataPlaneOnly(t *testing.T) {
	g := chainGateway(t, "alice", &stubKeyDatabase{})

	token, _, err := g.authService.GenerateJWT("alice", "0xWallet", time.Hour, nil)
	if err != nil {
		t.Fatalf("mint a wallet token: %v", err)
	}
	withJWT := map[string]string{"Authorization": "Bearer " + token}

	if status, body, reached := serve(g, chainRequest(http.MethodPost, "/v1/storage/upload", withJWT)); !reached {
		t.Errorf("a signed-in wallet was refused its own namespace's storage: status=%d body=%s",
			status, strings.TrimSpace(body))
	}

	status, body, reached := serve(g, chainRequest(http.MethodGet, "/v1/deployments/list", withJWT))
	if reached {
		t.Error("a wallet JWT reached the control plane")
	}
	if status != http.StatusForbidden {
		t.Errorf("status = %d, want 403 (%s)", status, strings.TrimSpace(body))
	}
}

// A wallet signed in to another namespace is another namespace's caller,
// whatever its token says about itself.
func TestChain_aWalletJWTFromAnotherNamespaceIsRefused(t *testing.T) {
	g := chainGateway(t, "alice", &stubKeyDatabase{})

	token, _, err := g.authService.GenerateJWT("bob", "0xWallet", time.Hour, nil)
	if err != nil {
		t.Fatalf("mint a wallet token: %v", err)
	}

	status, body, reached := serve(g, chainRequest(http.MethodPost, "/v1/storage/upload",
		map[string]string{"Authorization": "Bearer " + token}))
	if reached {
		t.Fatal("a token issued for bob reached alice's gateway")
	}
	if status != http.StatusForbidden {
		t.Errorf("status = %d, want 403 (%s)", status, strings.TrimSpace(body))
	}
}
