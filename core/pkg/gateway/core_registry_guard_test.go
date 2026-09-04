package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/gateway/auth"
	"github.com/DeBrosOfficial/network/pkg/gateway/ctxkeys"
	"github.com/DeBrosOfficial/network/pkg/logging"
	"github.com/DeBrosOfficial/network/pkg/rqlite"
)

// operatorRegistry answers the operator-list question and nothing else.
type operatorRegistry struct {
	rqlite.Client

	operators map[string]bool
	failQuery bool
}

func (o *operatorRegistry) Query(_ context.Context, dest any, query string, args ...any) error {
	if o.failQuery {
		return errRegistry
	}
	if !strings.Contains(query, "FROM operators") {
		// The wallet lookup for an API-key caller; no rows means no wallet.
		return nil
	}
	wallet, _ := args[0].(string)
	if o.operators[wallet] {
		rows := reflect.ValueOf(dest).Elem()
		row := reflect.New(rows.Type().Elem()).Elem()
		row.Field(0).SetString(wallet)
		rows.Set(reflect.Append(rows, row))
	}
	return nil
}

type errString string

func (e errString) Error() string { return string(e) }

const errRegistry errString = "registry unreachable"

func registryGateway(t *testing.T, namespace string, operators ...string) (*Gateway, *operatorRegistry) {
	t.Helper()
	logger, _ := logging.NewColoredLogger(logging.ComponentGateway, false)
	db := &operatorRegistry{operators: map[string]bool{}}
	for _, w := range operators {
		db.operators[strings.ToLower(w)] = true
	}
	return &Gateway{
		logger:    logger,
		ormClient: db,
		cfg:       &Config{ClientNamespace: namespace, BaseDomain: "dbrs.space"},
	}, db
}

func walletRequest(path, wallet string) *http.Request {
	r := httptest.NewRequest(http.MethodGet, path, nil)
	if wallet != "" {
		r = r.WithContext(context.WithValue(r.Context(), ctxkeys.JWT, &auth.JWTClaims{Sub: wallet}))
	}
	return r
}

// The bug: the gateway that fronts the cluster serves the registry —
// api_keys, namespace_ownership, refresh_tokens, wireguard_peers,
// deployment_env_vars, invite_tokens — and any tenant's admin key could
// export it.
func TestCoreRegistryGuard_refusesATenantOnTheClusterGateway(t *testing.T) {
	g, _ := registryGateway(t, "default", "0xoperator")

	for _, path := range []string{
		"/v1/rqlite/export", "/v1/rqlite/import", "/v1/rqlite/query",
		"/v1/rqlite/exec", "/v1/rqlite", "/rqlite",
	} {
		w := httptest.NewRecorder()
		if g.requireOperatorForCoreRegistry(w, walletRequest(path, "0xtenant")) {
			t.Errorf("%s let a tenant through", path)
		}
		if w.Code != http.StatusForbidden {
			t.Errorf("%s answered %d, want 403", path, w.Code)
		}
	}
}

func TestCoreRegistryGuard_allowsAnOperator(t *testing.T) {
	g, _ := registryGateway(t, "default", "0xoperator")

	w := httptest.NewRecorder()
	if !g.requireOperatorForCoreRegistry(w, walletRequest("/v1/rqlite/export", "0xoperator")) {
		t.Fatalf("an operator was refused: %d %s", w.Code, w.Body.String())
	}
}

// A namespace gateway's database is the tenant's own, and ownership of that
// namespace is already required. Applying the operator rule there would stop
// every tenant backing up their own data.
func TestCoreRegistryGuard_doesNotApplyToANamespaceGateway(t *testing.T) {
	g, _ := registryGateway(t, "anchat", "0xoperator")

	w := httptest.NewRecorder()
	if !g.requireOperatorForCoreRegistry(w, walletRequest("/v1/rqlite/export", "0xtenant")) {
		t.Fatalf("a tenant was refused on their own namespace gateway: %d %s", w.Code, w.Body.String())
	}
}

// Everything else on the cluster gateway is unaffected.
func TestCoreRegistryGuard_doesNotApplyToOtherPaths(t *testing.T) {
	g, _ := registryGateway(t, "default", "0xoperator")

	for _, path := range []string{"/v1/storage/upload", "/v1/cache/get", "/v1/health", "/v1/rqlited"} {
		w := httptest.NewRecorder()
		if !g.requireOperatorForCoreRegistry(w, walletRequest(path, "0xtenant")) {
			t.Errorf("%s was refused", path)
		}
	}
}

// Not knowing whether someone is an operator is not permission to hand them
// the registry.
func TestCoreRegistryGuard_deniesWhenTheListCannotBeRead(t *testing.T) {
	g, db := registryGateway(t, "default")
	db.failQuery = true

	w := httptest.NewRecorder()
	if g.requireOperatorForCoreRegistry(w, walletRequest("/v1/rqlite/export", "0xanyone")) {
		t.Fatal("an unreadable operator list let a caller read the registry")
	}
	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("status %d, want 503", w.Code)
	}
}

// The refusal has to say where the request should have gone, or a tenant
// trying to back up their own database is simply stuck.
func TestCoreRegistryGuard_namesTheNamespaceGateway(t *testing.T) {
	g, _ := registryGateway(t, "default", "0xoperator")

	r := walletRequest("/v1/rqlite/export", "0xtenant")
	r = r.WithContext(context.WithValue(r.Context(), CtxKeyNamespaceOverride, "anchat"))

	w := httptest.NewRecorder()
	g.requireOperatorForCoreRegistry(w, r)

	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	message, _ := body["error"].(string)
	if !strings.Contains(message, "https://ns-anchat.dbrs.space") {
		t.Errorf("the refusal does not name the namespace gateway: %q", message)
	}
}

func TestIsCoreRegistryPath(t *testing.T) {
	for _, path := range []string{"/rqlite", "/v1/rqlite", "/v1/rqlite/export", "/v1/rqlite/query"} {
		if !isCoreRegistryPath(path) {
			t.Errorf("%s is not treated as a raw-database path", path)
		}
	}
	for _, path := range []string{"/v1/rqlited", "/v1/db/query", "/v1/health", "/rqlited"} {
		if isCoreRegistryPath(path) {
			t.Errorf("%s is treated as a raw-database path", path)
		}
	}
}

// A gateway with no namespace configured is the cluster gateway, not a tenant's
// — treating it as a tenant's would leave the registry open.
func TestServesCoreRegistry(t *testing.T) {
	for ns, want := range map[string]bool{"": true, "default": true, "anchat": false, "index": false} {
		g, _ := registryGateway(t, ns)
		if got := g.servesCoreRegistry(); got != want {
			t.Errorf("client_namespace %q: servesCoreRegistry = %v, want %v", ns, got, want)
		}
	}
}

// The guard has to be in the chain, not merely written. A test that calls it
// directly would not notice it being dropped from authorizationMiddleware.
func TestAuthorizationMiddleware_refusesRegistryAccessFromATenant(t *testing.T) {
	g, _ := registryGateway(t, "default", "0xoperator")

	var reached bool
	chain := g.authorizationMiddleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		reached = true
		w.WriteHeader(http.StatusOK)
	}))

	w := httptest.NewRecorder()
	chain.ServeHTTP(w, walletRequest("/v1/rqlite/export", "0xtenant"))

	if reached {
		t.Fatal("a tenant reached the registry export handler")
	}
	if w.Code != http.StatusForbidden {
		t.Errorf("status %d, want 403: %s", w.Code, strings.TrimSpace(w.Body.String()))
	}
}
