package gateway

import (
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/pkg/logging"
)

const (
	routingBaseDomain = "orama.test"
	tenantNamespace   = "tenant-a"
)

// upstreamHit is what the fake namespace gateway saw of a proxied request.
type upstreamHit struct {
	path      string
	namespace string
}

// clusterGatewayFixture is a cluster gateway (client_namespace "default")
// whose tenant-a namespace gateway is a local test server.
type clusterGatewayFixture struct {
	g        *Gateway
	hits     []upstreamHit
	local    int
	lastPass *http.Request
}

func newClusterGatewayFixture(t *testing.T, clientNamespace string) *clusterGatewayFixture {
	t.Helper()
	f := &clusterGatewayFixture{}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.hits = append(f.hits, upstreamHit{path: r.URL.Path, namespace: r.Header.Get(HeaderInternalAuthNamespace)})
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(upstream.Close)
	host, portStr, err := net.SplitHostPort(strings.TrimPrefix(upstream.URL, "http://"))
	if err != nil {
		t.Fatalf("split upstream address: %v", err)
	}
	port, _ := strconv.Atoi(portStr)

	logger, _ := logging.NewColoredLogger(logging.ComponentGateway, false)
	cache := newMiddlewareCache(time.Minute)
	t.Cleanup(cache.Stop)
	cache.SetNamespaceTargets(tenantNamespace, []gatewayTarget{{ip: host, port: port}})

	f.g = &Gateway{
		logger:          logger,
		cfg:             &Config{ClientNamespace: clientNamespace, BaseDomain: routingBaseDomain},
		authService:     newAuthServiceForTest(t),
		mwCache:         cache,
		circuitBreakers: NewCircuitBreakerRegistry(),
		proxyTransport:  &http.Transport{},
		internalAuthKey: testHopKey(t),
	}
	return f
}

// serve runs r through the routing middleware; a request it keeps is counted
// as served locally.
func (f *clusterGatewayFixture) serve(r *http.Request) *httptest.ResponseRecorder {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.local++
		f.lastPass = r
		w.WriteHeader(http.StatusOK)
	})
	rec := httptest.NewRecorder()
	f.g.clusterServerlessRoutingMiddleware(next).ServeHTTP(rec, r)
	return rec
}

func (f *clusterGatewayFixture) bearer(t *testing.T, r *http.Request, namespace string) *http.Request {
	t.Helper()
	token, _, err := f.g.authService.GenerateJWT(namespace, "0xwallet", time.Minute, nil)
	if err != nil {
		t.Fatalf("GenerateJWT: %v", err)
	}
	r.Header.Set("Authorization", "Bearer "+token)
	return r
}

// bugboard #427: the cluster gateway ran tenant functions against the cluster
// registry. A tenant's invocation now reaches the tenant's own gateway.
func TestClusterServerlessRouting_tenantInvokeIsProxied(t *testing.T) {
	f := newClusterGatewayFixture(t, "default")

	rec := f.serve(httptest.NewRequest(http.MethodPost, "/v1/invoke/"+tenantNamespace+"/hello", nil))

	if rec.Code != http.StatusOK || len(f.hits) != 1 || f.local != 0 {
		t.Fatalf("status %d, upstream hits %d, served locally %d; want the tenant's gateway to serve it",
			rec.Code, len(f.hits), f.local)
	}
	if f.hits[0].path != "/v1/invoke/"+tenantNamespace+"/hello" {
		t.Errorf("proxied path = %q", f.hits[0].path)
	}
}

// Management names no namespace; it goes where the credential belongs, and
// the namespace gateway is told whose it is.
func TestClusterServerlessRouting_managementGoesToTheCredentialsNamespace(t *testing.T) {
	f := newClusterGatewayFixture(t, "default")

	for _, r := range []*http.Request{
		f.bearer(t, httptest.NewRequest(http.MethodGet, "/v1/functions", nil), tenantNamespace),
		f.bearer(t, httptest.NewRequest(http.MethodPost, "/v1/functions/hello/invoke", nil), tenantNamespace),
		f.bearer(t, httptest.NewRequest(http.MethodPut, "/v1/functions/secrets", nil), tenantNamespace),
	} {
		f.serve(r)
	}
	f.g.mwCache.SetAPIKeyEntry("ak_tenant", tenantNamespace, "admin")
	keyed := httptest.NewRequest(http.MethodDelete, "/v1/functions/hello", nil)
	keyed.Header.Set("X-API-Key", "ak_tenant")
	f.serve(keyed)

	if f.local != 0 || len(f.hits) != 4 {
		t.Fatalf("served locally %d, proxied %d; want all 4 proxied", f.local, len(f.hits))
	}
	for _, hit := range f.hits {
		if hit.namespace != tenantNamespace {
			t.Errorf("%s reached the namespace gateway as %q, want %q", hit.path, hit.namespace, tenantNamespace)
		}
	}
}

// A credential only ever acts in its own namespace: naming another one is
// refused before anything is proxied or run.
func TestClusterServerlessRouting_credentialOfAnotherNamespaceIsRefused(t *testing.T) {
	f := newClusterGatewayFixture(t, "default")

	for _, r := range []*http.Request{
		f.bearer(t, httptest.NewRequest(http.MethodPost, "/v1/invoke/"+tenantNamespace+"/hello", nil), "tenant-b"),
		f.bearer(t, httptest.NewRequest(http.MethodGet, "/v1/functions?namespace="+tenantNamespace, nil), "tenant-b"),
	} {
		if rec := f.serve(r); rec.Code != http.StatusForbidden {
			t.Errorf("%s: status %d, want 403", r.URL, rec.Code)
		}
	}
	if len(f.hits) != 0 || f.local != 0 {
		t.Errorf("a refused request was proxied %d / served %d times", len(f.hits), f.local)
	}
}

// The default namespace's own functions are this gateway's, and stay here.
func TestClusterServerlessRouting_defaultStaysLocal(t *testing.T) {
	f := newClusterGatewayFixture(t, "default")

	f.serve(httptest.NewRequest(http.MethodPost, "/v1/invoke/default/hello", nil))
	f.serve(f.bearer(t, httptest.NewRequest(http.MethodGet, "/v1/functions", nil), "default"))

	if f.local != 2 || len(f.hits) != 0 {
		t.Errorf("served locally %d, proxied %d; want 2 local", f.local, len(f.hits))
	}
}

// Deciding where a request goes must not change it: a WebSocket that stays
// here is authenticated again by authMiddleware, which reads `?jwt=`.
func TestClusterServerlessRouting_localWebSocketKeepsItsQueryToken(t *testing.T) {
	f := newClusterGatewayFixture(t, "default")
	token, _, err := f.g.authService.GenerateJWT("default", "0xwallet", time.Minute, nil)
	if err != nil {
		t.Fatalf("GenerateJWT: %v", err)
	}
	r := httptest.NewRequest(http.MethodGet, "/v1/functions/rpc/ws?jwt="+token, nil)
	r.Header.Set("Connection", "upgrade")
	r.Header.Set("Upgrade", "websocket")

	f.serve(r)

	if f.local != 1 || f.lastPass.URL.Query().Get("jwt") != token {
		t.Errorf("served locally %d; the token did not survive to authMiddleware", f.local)
	}
}

// A proxied WebSocket carries its identity in the signed headers, not in the
// URL the namespace gateway logs.
func TestClusterServerlessRouting_proxiedWebSocketDropsItsQueryToken(t *testing.T) {
	f := newClusterGatewayFixture(t, "default")
	token, _, err := f.g.authService.GenerateJWT(tenantNamespace, "0xwallet", time.Minute, nil)
	if err != nil {
		t.Fatalf("GenerateJWT: %v", err)
	}
	r := httptest.NewRequest(http.MethodGet, "/v1/functions/rpc/ws?jwt="+token+"&keep=1", nil)
	r.Header.Set("Connection", "upgrade")
	r.Header.Set("Upgrade", "websocket")

	// The recorder cannot be hijacked, so the tunnel itself fails; what is
	// checked is the request as it was about to cross the hop.
	f.serve(r)

	if f.local != 0 {
		t.Fatal("a tenant WebSocket was served locally")
	}
	if r.URL.Query().Get("jwt") != "" || r.URL.Query().Get("keep") != "1" {
		t.Errorf("RawQuery at the hop = %q; want the token dropped and the rest kept", r.URL.RawQuery)
	}
}

// An anonymous caller may invoke a public function, but has to say whose.
func TestClusterServerlessRouting_anonymousRequestNamingNoNamespaceIsRefused(t *testing.T) {
	f := newClusterGatewayFixture(t, "default")

	rec := f.serve(httptest.NewRequest(http.MethodPost, "/v1/functions/hello/invoke", nil))

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status %d, want 400", rec.Code)
	}
	var body struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode refusal: %v", err)
	}
	if !strings.Contains(body.Error.Message, "https://ns-<namespace>."+routingBaseDomain) {
		t.Errorf("the refusal does not point at the namespace gateway: %q", body.Error.Message)
	}
	if f.local != 0 || len(f.hits) != 0 {
		t.Error("a request naming no namespace was served")
	}
}

func TestClusterServerlessRouting_credentialFailures(t *testing.T) {
	f := newClusterGatewayFixture(t, "default")

	if rec := f.serve(httptest.NewRequest(http.MethodGet, "/v1/functions", nil)); rec.Code != http.StatusUnauthorized {
		t.Errorf("management with no credential: status %d, want 401", rec.Code)
	}
	bad := httptest.NewRequest(http.MethodGet, "/v1/functions", nil)
	bad.Header.Set("X-API-Key", "ak_unknown")
	if rec := f.serve(bad); rec.Code != http.StatusUnauthorized {
		t.Errorf("management with an unknown key: status %d, want 401", rec.Code)
	}
	if f.local != 0 || len(f.hits) != 0 {
		t.Error("an unauthenticated request was served")
	}
}

// A namespace gateway's functions are its namespace's; it routes nothing.
func TestClusterServerlessRouting_namespaceGatewayIsUntouched(t *testing.T) {
	f := newClusterGatewayFixture(t, tenantNamespace)

	f.serve(httptest.NewRequest(http.MethodPost, "/v1/invoke/tenant-b/hello", nil))
	f.serve(httptest.NewRequest(http.MethodGet, "/v1/functions", nil))

	if f.local != 2 || len(f.hits) != 0 {
		t.Errorf("served locally %d, proxied %d; want 2 local", f.local, len(f.hits))
	}
}

func TestClusterServerlessRouting_otherRoutesAreUntouched(t *testing.T) {
	f := newClusterGatewayFixture(t, "default")

	f.serve(httptest.NewRequest(http.MethodGet, "/v1/storage/get/bafy", nil))
	f.serve(httptest.NewRequest(http.MethodGet, "/v1/functionsx", nil))
	f.serve(httptest.NewRequest(http.MethodOptions, "/v1/invoke/"+tenantNamespace+"/hello", nil))

	if f.local != 3 || len(f.hits) != 0 {
		t.Errorf("served locally %d, proxied %d; want 3 local", f.local, len(f.hits))
	}
}

func TestIsServerlessRoute_matchesTheRegisteredRoutes(t *testing.T) {
	for path, want := range map[string]bool{
		"/v1/functions":                   true,
		"/v1/functions/hello/invoke":      true,
		"/v1/functions/secrets":           true,
		"/v1/invoke/ns/fn":                true,
		"/v1/serverless/ws/connections":   true,
		"/v1/serverless/ws/connections/x": true,
		"/v1/functionsx":                  false,
		"/v1/invoke":                      false,
		"/v1/storage/get/x":               false,
		"/":                               false,
	} {
		if got := isServerlessRoute(path); got != want {
			t.Errorf("isServerlessRoute(%q) = %v, want %v", path, got, want)
		}
	}
}

func TestNamespaceNamedByServerlessRequest(t *testing.T) {
	for target, want := range map[string]string{
		"/v1/invoke/acme/fn":                  "acme",
		"/v1/invoke/acme/fn?namespace=other":  "acme",
		"/v1/invoke//fn":                      "",
		"/v1/functions?namespace=acme":        "acme",
		"/v1/functions/fn/invoke?namespace=a": "a",
		"/v1/functions":                       "",
	} {
		if got := namespaceNamedByServerlessRequest(httptest.NewRequest(http.MethodGet, target, nil)); got != want {
			t.Errorf("%s: named %q, want %q", target, got, want)
		}
	}
}

// withMiddleware is the chain the server runs; a routing middleware that is
// not in it protects nothing.
func TestWithMiddleware_routesTenantServerlessToTheNamespaceGateway(t *testing.T) {
	f := newClusterGatewayFixture(t, "default")
	f.g.ready = newReadiness()
	f.g.ready.set(ReadinessReady, "ready")
	f.g.startedAt = time.Now()

	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		f.local++
		w.WriteHeader(http.StatusOK)
	})
	rec := httptest.NewRecorder()
	f.g.withMiddleware(next).ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/invoke/"+tenantNamespace+"/hello", nil))

	if len(f.hits) != 1 || f.local != 0 {
		t.Errorf("status %d, proxied %d, served locally %d; want the tenant's gateway to serve it",
			rec.Code, len(f.hits), f.local)
	}
}
