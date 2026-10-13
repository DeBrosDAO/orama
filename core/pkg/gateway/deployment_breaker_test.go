package gateway

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"

	"github.com/DeBrosOfficial/network/pkg/deployments"
	"github.com/DeBrosOfficial/network/pkg/httputil"
	"github.com/DeBrosOfficial/network/pkg/logging"
)

func deploymentGateway(t *testing.T) *Gateway {
	t.Helper()
	return &Gateway{
		logger:          newRQLiteTestLogger(),
		cfg:             &Config{},
		nodePeerID:      "peer-a",
		circuitBreakers: NewCircuitBreakerRegistry(),
		proxyTransport:  &http.Transport{},
	}
}

func appDeployment(id, name string) *deployments.Deployment {
	return &deployments.Deployment{ID: id, Namespace: "acme", Name: name, HomeNodeID: "peer-b"}
}

func forwardHome(g *Gateway, d *deployments.Deployment, hostPort string) (*httptest.ResponseRecorder, bool) {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.Host = d.Name + ".example.org"
	rec := httptest.NewRecorder()
	served := g.forwardToHomeNode(rec, r, d, "127.0.0.1", hostPort, time.Second)
	return rec, served
}

func hostPort(s *httptest.Server) string { return strings.TrimPrefix(s.URL, "http://") }

// The breaker toward a home node was one per node: one app answering 503 took
// every other app on that node out of rotation for the nodes that forwarded to
// it.
func TestForwardToHomeNode_oneAppsFailureDoesNotOpenAnotherAppsCircuit(t *testing.T) {
	// The home node's gateway: shop's local process is down (the node's own 503,
	// no marker), blog is fine.
	home := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.Host, "shop.") {
			w.WriteHeader(http.StatusServiceUnavailable)
		}
	}))
	defer home.Close()
	g := deploymentGateway(t)
	shop, blog := appDeployment("dep-shop", "shop"), appDeployment("dep-blog", "blog")

	for i := 0; i < defaultFailureThreshold; i++ {
		forwardHome(g, shop, hostPort(home))
	}
	if st := g.circuitBreakers.ForDeployment("dep-shop", "acme", "shop", "127.0.0.1").State(); st != CircuitOpen {
		t.Fatalf("shop's circuit is %v after the node failed it %d times, want open", st, defaultFailureThreshold)
	}
	if _, served := forwardHome(g, shop, hostPort(home)); served {
		t.Fatal("shop was forwarded through an open circuit")
	}

	rec, served := forwardHome(g, blog, hostPort(home))
	if !served || rec.Code != http.StatusOK {
		t.Fatalf("blog got %d (served=%v) while shop's circuit was open; one app's failure refused another's request", rec.Code, served)
	}
	if got := g.circuitBreakers.Unhealthy(breakerReportIdle); len(got) != 1 || got[0].Deployment != "shop" || got[0].Namespace != "acme" {
		t.Errorf("breakers not closed = %+v, want only shop's", got)
	}
}

// What the app answers is its own: the node that ran it marks the answer, and
// the node that forwarded does not hold a 502, 503 or 504 with the mark against
// the node, nor show the mark to the client.
func TestForwardToHomeNode_anAppsOwn503IsNotTheNodesFailure(t *testing.T) {
	home := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set(httputil.HeaderTenantOrigin, "1")
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer home.Close()
	g := deploymentGateway(t)
	shop := appDeployment("dep-shop", "shop")

	for i := 0; i < 3*defaultFailureThreshold; i++ {
		rec, served := forwardHome(g, shop, hostPort(home))
		if !served || rec.Code != http.StatusServiceUnavailable {
			t.Fatalf("request %d: %d served=%v, want the app's 503 relayed", i, rec.Code, served)
		}
		if rec.Header().Get(httputil.HeaderTenantOrigin) != "" {
			t.Fatal("the internal tenant-origin marker reached the client")
		}
	}
	if st := g.circuitBreakers.ForDeployment("dep-shop", "acme", "shop", "127.0.0.1").State(); st != CircuitClosed {
		t.Fatalf("circuit is %v after the app answered 503 repeatedly, want closed", st)
	}
}

// A replica's own proxy failing is passed over for the next replica; its app
// answering is final and relayed.
func TestForwardToReplica_theNodesFailureIsSkippedTheAppsAnswerIsRelayed(t *testing.T) {
	platform := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer platform.Close()
	app := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set(httputil.HeaderTenantOrigin, "1")
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer app.Close()
	g := deploymentGateway(t)
	shop := appDeployment("dep-shop", "shop")
	r := httptest.NewRequest(http.MethodGet, "/", nil)

	if g.forwardToReplica(httptest.NewRecorder(), r, shop, "127.0.0.1", hostPort(platform)) {
		t.Error("a replica whose own proxy answered 502 was taken as serving the request")
	}
	if st := g.circuitBreakers.ForDeployment("dep-shop", "acme", "shop", "127.0.0.1").State(); st != CircuitClosed {
		t.Fatalf("circuit is %v after one failure", st)
	}
	rec := httptest.NewRecorder()
	if !g.forwardToReplica(rec, r, shop, "127.0.0.1", hostPort(app)) || rec.Code != http.StatusBadGateway {
		t.Errorf("an app's 502 was not relayed: %d", rec.Code)
	}
	if rec.Header().Get(httputil.HeaderTenantOrigin) != "" {
		t.Error("the marker reached the client")
	}
	for i := 0; i < defaultFailureThreshold; i++ {
		g.forwardToReplica(httptest.NewRecorder(), r, shop, "127.0.0.1", hostPort(platform))
	}
	if st := g.circuitBreakers.ForDeployment("dep-shop", "acme", "shop", "127.0.0.1").State(); st != CircuitOpen {
		t.Errorf("circuit is %v after the replica's proxy failed repeatedly, want open", st)
	}
}

func TestForwardToHomeNode_aRefusedConnectionCounts(t *testing.T) {
	g := deploymentGateway(t)
	shop := appDeployment("dep-shop", "shop")
	dead := "127.0.0.1:" + strconv.Itoa(freePort(t))
	for i := 0; i < defaultFailureThreshold; i++ {
		if _, served := forwardHome(g, shop, dead); served {
			t.Fatal("a refused connection was served")
		}
	}
	got := g.circuitBreakers.Unhealthy(breakerReportIdle)
	if len(got) != 1 || got[0].State != CircuitOpen || !strings.Contains(got[0].LastError, "connection refused") ||
		strings.Contains(got[0].LastError, "http://") {
		t.Fatalf("unhealthy = %+v, want shop open with the refusal and no URL", got)
	}
}

// A body the client failed to deliver is not the node's fault.
func TestForwardToHomeNode_aBodyTheClientDroppedIsNotTheNodesFailure(t *testing.T) {
	home := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = r.Body.Read(make([]byte, 64))
		_, _ = r.Body.Read(make([]byte, 64))
	}))
	defer home.Close()
	g := deploymentGateway(t)
	shop := appDeployment("dep-shop", "shop")
	for i := 0; i < 2*defaultFailureThreshold; i++ {
		r := httptest.NewRequest(http.MethodPost, "/upload", &failingBody{})
		r.ContentLength = -1
		rec := httptest.NewRecorder()
		g.forwardToHomeNode(rec, r, shop, "127.0.0.1", hostPort(home), time.Second)
	}
	if st := g.circuitBreakers.ForDeployment("dep-shop", "acme", "shop", "127.0.0.1").State(); st != CircuitClosed {
		t.Fatalf("circuit is %v after clients dropped their uploads, want closed", st)
	}
}

// The node that runs the app tells the node that forwarded whose answer it is.
// A client's own request gets no marker.
func TestProxyToDynamicDeployment_marksTheAppsAnswerForAForwardingNode(t *testing.T) {
	app := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer app.Close()
	port, _ := strconv.Atoi(app.URL[strings.LastIndex(app.URL, ":")+1:])
	g := deploymentGateway(t)
	g.nodePeerID = "" // this node is the home node
	d := &deployments.Deployment{ID: "dep-shop", Namespace: "acme", Name: "shop", Port: port}

	forwarded := httptest.NewRequest(http.MethodGet, "/", nil)
	forwarded.Header.Set("X-Orama-Proxy-Node", "peer-a")
	rec := httptest.NewRecorder()
	g.proxyToDynamicDeployment(rec, forwarded, d)
	if rec.Code != http.StatusServiceUnavailable || rec.Header().Get(httputil.HeaderTenantOrigin) == "" {
		t.Errorf("forwarded request: %d with marker %q, want the app's 503 marked", rec.Code, rec.Header().Get(httputil.HeaderTenantOrigin))
	}

	direct := httptest.NewRecorder()
	g.proxyToDynamicDeployment(direct, httptest.NewRequest(http.MethodGet, "/", nil), d)
	if direct.Code != http.StatusServiceUnavailable || direct.Header().Get(httputil.HeaderTenantOrigin) != "" {
		t.Errorf("client request: %d with marker %q, want an unmarked 503", direct.Code, direct.Header().Get(httputil.HeaderTenantOrigin))
	}
}

// Whose process is down is the node's failure: nothing answered, so no mark.
func TestProxyToDynamicDeployment_aDownProcessIsNotMarked(t *testing.T) {
	g := deploymentGateway(t)
	g.nodePeerID = ""
	d := &deployments.Deployment{ID: "dep-shop", Namespace: "acme", Name: "shop", Port: freePort(t)}
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.Header.Set("X-Orama-Proxy-Node", "peer-a")
	rec := httptest.NewRecorder()
	g.proxyToDynamicDeployment(rec, r, d)
	if rec.Code != http.StatusServiceUnavailable || rec.Header().Get(httputil.HeaderTenantOrigin) != "" {
		t.Errorf("%d with marker %q, want an unmarked 503 the forwarding node counts", rec.Code, rec.Header().Get(httputil.HeaderTenantOrigin))
	}
}

func TestDeploymentBreakerKey_namesTheDeploymentAndTheNode(t *testing.T) {
	r := NewCircuitBreakerRegistry()
	a := r.ForDeployment("dep-1", "acme", "shop", "10.0.0.2")
	if r.ForDeployment("dep-1", "acme", "shop", "10.0.0.2") != a ||
		r.ForDeployment("dep-2", "acme", "blog", "10.0.0.2") == a ||
		r.ForDeployment("dep-1", "acme", "shop", "10.0.0.3") == a {
		t.Error("deployment breakers are not one per deployment and node")
	}
	if deploymentBreakerKey("acme", "10.0.0.2") == namespaceBreakerKey("acme", "10.0.0.2") {
		t.Error("a deployment's key collides with a namespace gateway's")
	}
}

// A deleted deployment's breakers go with the idle prune; the namespace
// gateways' member retention does not touch a deployment's.
func TestRegistry_aDeletedDeploymentsBreakersArePruned(t *testing.T) {
	r := NewCircuitBreakerRegistry()
	gone := r.ForDeployment("dep-gone", "acme", "old", "10.0.0.2")
	keep := r.ForDeployment("dep-live", "acme", "new", "10.0.0.2")
	r.ForNamespaceGateway("acme", "10.0.0.2")
	gone.mu.Lock()
	gone.lastUsed = time.Now().Add(-time.Hour)
	gone.mu.Unlock()

	if r.RetainNamespaceMembers("acme", []string{"10.0.0.2"}) != 0 || r.Len() != 3 {
		t.Fatal("retaining a namespace's members dropped a deployment's breaker")
	}
	if dropped := r.Prune(breakerIdleTTL); dropped != 1 || r.ForDeployment("dep-live", "acme", "new", "10.0.0.2") != keep {
		t.Fatalf("pruned %d, want only the deleted deployment's", dropped)
	}
}

func TestLogBreakerTransition_namesTheDeployment(t *testing.T) {
	core, logs := observer.New(zapcore.InfoLevel)
	g := deploymentGateway(t)
	g.logger = &logging.ColoredLogger{Logger: zap.New(core)}
	g.circuitBreakers.SetObserver(g.logBreakerTransition)
	cb := g.circuitBreakers.ForDeployment("dep-shop", "acme", "shop", "10.0.0.2")
	for i := 0; i < defaultFailureThreshold; i++ {
		cb.RecordFailure("connection refused")
	}
	entries := logs.FilterMessageSnippet("circuit breaker opened").All()
	if len(entries) != 1 {
		t.Fatalf("%d opening lines, want 1", len(entries))
	}
	f := entries[0].ContextMap()
	if f["namespace"] != "acme" || f["deployment"] != "shop" || f["node"] != "10.0.0.2" || f["last_error"] != "connection refused" {
		t.Errorf("opening line fields = %v", f)
	}
}

func TestBreakersReport_carriesTheDeploymentName(t *testing.T) {
	g := &Gateway{circuitBreakers: NewCircuitBreakerRegistry()}
	cb := g.circuitBreakers.ForDeployment("dep-shop", "acme", "shop", "10.0.0.2")
	for i := 0; i < defaultFailureThreshold; i++ {
		cb.RecordFailure("x")
	}
	r := g.breakersReport()
	if r == nil || len(r.Unhealthy) != 1 || r.Unhealthy[0].Deployment != "shop" || r.Unhealthy[0].Namespace != "acme" {
		t.Fatalf("report = %+v, want shop of acme", r)
	}
}
