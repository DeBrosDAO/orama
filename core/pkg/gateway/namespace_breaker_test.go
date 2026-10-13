package gateway

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/httputil"
	"github.com/DeBrosOfficial/network/pkg/telemetry/report"
	"github.com/gorilla/websocket"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"

	"github.com/DeBrosOfficial/network/pkg/logging"
)

// proxyOnce sends one request for namespace through g's namespace proxy and
// returns the response.
func proxyOnce(g *Gateway, namespace string, r *http.Request) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	g.proxyToNamespaceGateway(rec, r, namespace, namespaceProxyAuth{namespace: namespace})
	return rec
}

func get(path string) *http.Request { return httptest.NewRequest(http.MethodGet, path, nil) }

// openBreaker opens cb and lets its next Allow admit a probe at once.
func openBreaker(cb *CircuitBreaker) {
	for i := 0; i < defaultFailureThreshold; i++ {
		cb.RecordFailure("test")
	}
	cb.mu.Lock()
	cb.openDuration = 0
	cb.mu.Unlock()
}

// The breakers used to be keyed by node: a tenant whose gateway was down or
// overloaded took every other tenant on that node out of rotation (stagenet
// froakie, 2026-10-10: "all upstream circuits are open" for namespaces whose
// gateways were fine).
func TestNamespaceProxy_aFailingGatewayDoesNotOpenAnotherNamespacesCircuit(t *testing.T) {
	var betaHits int
	beta := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { betaHits++ }))
	defer beta.Close()
	// acme's gateway and beta's are both "on" 127.0.0.1; acme's is not running.
	g := proxyGateway(t, gatewayTarget{ip: "127.0.0.1", port: freePort(t)})
	g.mwCache.SetNamespaceTargets("beta", []gatewayTarget{{ip: "127.0.0.1", port: serverPort(beta)}})

	for i := 0; i < defaultFailureThreshold+2; i++ {
		proxyOnce(g, "acme", get("/v1/functions"))
	}
	if st := g.circuitBreakers.ForNamespaceGateway("acme", "127.0.0.1").State(); st != CircuitOpen {
		t.Fatalf("acme's circuit is %v, want open after its gateway refused every request", st)
	}

	rec := proxyOnce(g, "beta", get("/v1/functions"))
	if rec.Code != http.StatusOK || betaHits != 1 {
		t.Fatalf("beta answered %d (%s) with %d hits; one namespace's dead gateway refused another's requests",
			rec.Code, rec.Body.String(), betaHits)
	}
	if got := g.circuitBreakers.Unhealthy(breakerReportIdle); len(got) != 1 || got[0].Namespace != "acme" {
		t.Errorf("breakers not closed = %+v, want only acme's", got)
	}
}

// A dead node is still found out fast: every namespace's first failures open
// that namespace's breaker, so the second request does not dial it again.
func TestNamespaceProxy_aDeadNodeOpensEachNamespacesCircuitOnItsOwnFailures(t *testing.T) {
	g := proxyGateway(t, gatewayTarget{ip: "127.0.0.1", port: freePort(t)})
	g.mwCache.SetNamespaceTargets("beta", []gatewayTarget{{ip: "127.0.0.1", port: freePort(t)}})
	for _, ns := range []string{"acme", "beta"} {
		for i := 0; i < defaultFailureThreshold; i++ {
			proxyOnce(g, ns, get("/v1/functions"))
		}
	}
	if got := g.circuitBreakers.Unhealthy(breakerReportIdle); len(got) != 2 {
		t.Fatalf("breakers not closed = %+v, want acme's and beta's", got)
	}
	rec := proxyOnce(g, "acme", get("/v1/functions"))
	if rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), "all upstream circuits are open") {
		t.Fatalf("got %d %s, want the open-circuit 503", rec.Code, rec.Body.String())
	}
}

// A function answers with a status it chose, or fails to load: neither is the
// gateway being unhealthy, and counting them let one function's 503 take its
// whole namespace out of rotation.
func TestNamespaceProxy_aFunctionsOwnGatewayStatusIsNotAFailureOfTheGateway(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/v1/invoke/") {
			w.Header().Set(httputil.HeaderTenantOrigin, "1")
		}
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer upstream.Close()
	g := proxyGateway(t, gatewayTarget{ip: "127.0.0.1", port: serverPort(upstream)})

	for i := 0; i < 3*defaultFailureThreshold; i++ {
		rec := proxyOnce(g, "acme", get("/v1/invoke/acme/checkout"))
		if rec.Code != http.StatusServiceUnavailable {
			t.Fatalf("request %d: status %d, want the function's 503 passed through", i, rec.Code)
		}
		if rec.Header().Get(httputil.HeaderTenantOrigin) != "" {
			t.Fatal("the internal function-origin marker reached the client")
		}
	}
	if st := g.circuitBreakers.ForNamespaceGateway("acme", "127.0.0.1").State(); st != CircuitClosed {
		t.Fatalf("circuit is %v after a function answered 503 %d times, want closed", st, 3*defaultFailureThreshold)
	}

	// The same status from the gateway itself still counts.
	for i := 0; i < defaultFailureThreshold; i++ {
		proxyOnce(g, "acme", get("/v1/health"))
	}
	if st := g.circuitBreakers.ForNamespaceGateway("acme", "127.0.0.1").State(); st != CircuitOpen {
		t.Fatalf("circuit is %v after the gateway answered 503 %d times, want open", st, defaultFailureThreshold)
	}
}

func TestIsUpstreamFailure_onlyTheGatewaysOwnGatewayErrors(t *testing.T) {
	mk := func(status int, fn bool) *http.Response {
		resp := &http.Response{StatusCode: status, Header: http.Header{}}
		if fn {
			resp.Header.Set(httputil.HeaderTenantOrigin, "1")
		}
		return resp
	}
	for _, tc := range []struct {
		name   string
		status int
		fn     bool
		want   bool
	}{
		{"502 from the gateway", 502, false, true},
		{"503 from the gateway", 503, false, true},
		{"504 from the gateway", 504, false, true},
		{"503 from a function", 503, true, false},
		{"502 from a function", 502, true, false},
		{"500 from the gateway", 500, false, false},
		{"429 from the gateway", 429, false, false},
		{"404 from the gateway", 404, false, false},
		{"401 from the gateway", 401, false, false},
		{"200", 200, false, false},
	} {
		if got := isUpstreamFailure(mk(tc.status, tc.fn)); got != tc.want {
			t.Errorf("%s: isUpstreamFailure = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// A request body the client failed to deliver is the client's failure: five
// dropped uploads must not take the namespace's gateway out of rotation.
type failingBody struct {
	sent bool
}

func (b *failingBody) Read(p []byte) (int, error) {
	if !b.sent {
		b.sent = true
		return copy(p, "partial"), nil
	}
	return 0, errors.New("client connection reset")
}

func TestNamespaceProxy_aBodyTheClientDroppedIsNotTheGatewaysFailure(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
	}))
	defer upstream.Close()
	g := proxyGateway(t, gatewayTarget{ip: "127.0.0.1", port: serverPort(upstream)})

	for i := 0; i < 2*defaultFailureThreshold; i++ {
		r := httptest.NewRequest(http.MethodPost, "/v1/storage/upload", &failingBody{})
		r.ContentLength = -1
		proxyOnce(g, "acme", r)
	}
	if st := g.circuitBreakers.ForNamespaceGateway("acme", "127.0.0.1").State(); st != CircuitClosed {
		t.Fatalf("circuit is %v after clients dropped their uploads, want closed", st)
	}
}

// A refused connection and a gateway answering 502/503/504 are the failures
// that count; 4xx and the gateway's own 500 are answers of a working gateway.
func TestNamespaceProxy_onlyAnUnhealthyGatewayCounts(t *testing.T) {
	for _, tc := range []struct {
		status int
		want   CircuitState
	}{
		{http.StatusBadGateway, CircuitOpen},
		{http.StatusServiceUnavailable, CircuitOpen},
		{http.StatusGatewayTimeout, CircuitOpen},
		{http.StatusBadRequest, CircuitClosed},
		{http.StatusUnauthorized, CircuitClosed},
		{http.StatusNotFound, CircuitClosed},
		{http.StatusTooManyRequests, CircuitClosed},
		{http.StatusInternalServerError, CircuitClosed},
	} {
		status := tc.status
		upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(status) }))
		g := proxyGateway(t, gatewayTarget{ip: "127.0.0.1", port: serverPort(upstream)})
		for i := 0; i < 2*defaultFailureThreshold; i++ {
			proxyOnce(g, "acme", get("/v1/functions"))
		}
		if st := g.circuitBreakers.ForNamespaceGateway("acme", "127.0.0.1").State(); st != tc.want {
			t.Errorf("after the gateway answered %d repeatedly the circuit is %v, want %v", tc.status, st, tc.want)
		}
		upstream.Close()
	}
}

// While a probe is in flight every other request is refused, and the probe's
// answer is what decides: success closes the circuit at once.
func TestNamespaceProxy_theHalfOpenProbeDecidesAsSoonAsItAnswers(t *testing.T) {
	release := make(chan struct{})
	probing := make(chan struct{}, 1)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		probing <- struct{}{}
		<-release
	}))
	defer upstream.Close()
	g := proxyGateway(t, gatewayTarget{ip: "127.0.0.1", port: serverPort(upstream)})
	cb := g.circuitBreakers.ForNamespaceGateway("acme", "127.0.0.1")
	openBreaker(cb)

	probe := make(chan int, 1)
	go func() { probe <- proxyOnce(g, "acme", get("/v1/functions")).Code }()
	<-probing

	if rec := proxyOnce(g, "acme", get("/v1/functions")); rec.Code != http.StatusServiceUnavailable ||
		!strings.Contains(rec.Body.String(), "all upstream circuits are open") {
		t.Fatalf("a request during the probe got %d %s, want the open-circuit 503", rec.Code, rec.Body.String())
	}
	close(release)
	if code := <-probe; code != http.StatusOK {
		t.Fatalf("the probe got %d", code)
	}
	if cb.State() != CircuitClosed {
		t.Fatalf("circuit is %v after a successful probe, want closed", cb.State())
	}
}

// A probe whose client left says nothing about the gateway, and must not hold
// the probe slot until halfOpenTimeout.
func TestNamespaceProxy_aProbeWhoseClientLeftGivesTheSlotBack(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer upstream.Close()
	g := proxyGateway(t, gatewayTarget{ip: "127.0.0.1", port: serverPort(upstream)})
	cb := g.circuitBreakers.ForNamespaceGateway("acme", "127.0.0.1")
	openBreaker(cb)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	proxyOnce(g, "acme", get("/v1/functions").WithContext(ctx))

	if st := cb.State(); st == CircuitHalfOpen {
		t.Fatal("the cancelled probe still holds the half-open slot")
	}
	rec := proxyOnce(g, "acme", get("/v1/functions"))
	if rec.Code != http.StatusOK {
		t.Fatalf("the next request got %d, want it admitted as the probe", rec.Code)
	}
	if cb.State() != CircuitClosed {
		t.Fatalf("circuit is %v, want closed", cb.State())
	}
}

// A WebSocket tunnel lasts as long as the client keeps it. The probe's outcome
// is the setup, not the end, or the slot is held for hours.
func TestNamespaceProxy_aWebSocketProbeIsDecidedWhenTheTunnelIsUp(t *testing.T) {
	hold := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer c.Close()
		<-hold
	}))
	defer upstream.Close()
	defer close(hold)
	g := proxyGateway(t, gatewayTarget{ip: "127.0.0.1", port: serverPort(upstream)})
	cb := g.circuitBreakers.ForNamespaceGateway("acme", "127.0.0.1")
	openBreaker(cb)
	front := frontFor(t, g)

	conn, _, err := websocket.DefaultDialer.Dial(wsURL(front), nil)
	if err != nil {
		t.Fatalf("the probe upgrade failed: %v", err)
	}
	defer conn.Close()
	if st := cb.State(); st != CircuitClosed {
		t.Fatalf("circuit is %v while the probe's tunnel is open, want closed", st)
	}
}

// An operator reads the story of an outage from the log: which namespace's
// gateway on which node, how many failures, and the last error.
func TestLogBreakerTransition_namesTheGatewayAndWhy(t *testing.T) {
	core, logs := observer.New(zapcore.InfoLevel)
	g := proxyGateway(t, gatewayTarget{ip: "127.0.0.1", port: freePort(t)})
	g.logger = &logging.ColoredLogger{Logger: zap.New(core)}
	g.circuitBreakers.SetObserver(g.logBreakerTransition)

	for i := 0; i < defaultFailureThreshold; i++ {
		proxyOnce(g, "acme", get("/v1/functions"))
	}
	var opened []observer.LoggedEntry
	for _, e := range logs.All() {
		if strings.Contains(e.Message, "circuit breaker opened") {
			opened = append(opened, e)
		}
	}
	if len(opened) != 1 {
		t.Fatalf("%d opening lines, want 1: %v", len(opened), logs.All())
	}
	e := opened[0]
	f := e.ContextMap()
	if e.Level != zapcore.WarnLevel || f["namespace"] != "acme" || f["node"] != "127.0.0.1" ||
		f["from"] != "closed" || f["to"] != "open" || f["failures"] != int64(defaultFailureThreshold) ||
		!strings.Contains(f["last_error"].(string), "connection refused") {
		t.Errorf("opening line = %v %v, want a warning naming acme, 127.0.0.1, 5 failures and the refusal", e.Level, f)
	}
}

func TestBreakersReport_isCappedAndCountsEverything(t *testing.T) {
	g := &Gateway{circuitBreakers: NewCircuitBreakerRegistry()}
	if g.breakersReport() != nil {
		t.Fatal("a gateway with no breakers reported some")
	}
	const open = 120
	for i := 0; i < open; i++ {
		cb := g.circuitBreakers.ForNamespaceGateway("ns"+string(rune('a'+i%26))+string(rune('a'+i/26)), "10.0.0.2")
		for j := 0; j < defaultFailureThreshold; j++ {
			cb.RecordFailure("connection refused")
		}
	}
	g.circuitBreakers.ForNamespaceGateway("healthy", "10.0.0.2")

	r := g.breakersReport()
	if r.Tracked != open+1 || r.NotClosed != open {
		t.Fatalf("report counts %d tracked, %d not closed; want %d and %d", r.Tracked, r.NotClosed, open+1, open)
	}
	if len(r.Unhealthy) != 50 || r.Unhealthy[0].State != "open" || r.Unhealthy[0].LastError != "connection refused" ||
		r.Unhealthy[0].Node != "10.0.0.2" || r.Unhealthy[0].LastFailure.IsZero() {
		t.Errorf("listed %d breakers, first %+v", len(r.Unhealthy), r.Unhealthy[0])
	}
}

// The breakers go into the node report the cluster gateway collects.
func TestDecorateNodeReport_carriesTheBreakers(t *testing.T) {
	g := &Gateway{circuitBreakers: NewCircuitBreakerRegistry()}
	cb := g.circuitBreakers.ForNamespaceGateway("acme", "10.0.0.2")
	for i := 0; i < defaultFailureThreshold; i++ {
		cb.RecordFailure("connection refused")
	}
	r := &report.NodeReport{}
	g.decorateNodeReport(r)
	if r.Breakers == nil || r.Breakers.NotClosed != 1 || r.Breakers.Unhealthy[0].Namespace != "acme" {
		t.Fatalf("node report breakers = %+v", r.Breakers)
	}
}

// Removing a namespace gives its breakers back the next time the proxy asks
// the registry for its gateways and finds none.
func TestNamespaceProxy_aRemovedNamespacesBreakersAreDropped(t *testing.T) {
	g := &Gateway{logger: newRQLiteTestLogger(), cfg: &Config{}, registry: targetsDB(t), circuitBreakers: NewCircuitBreakerRegistry()}
	g.circuitBreakers.ForNamespaceGateway("acme", "127.0.0.1")
	g.circuitBreakers.ForNamespaceGateway("acme", "127.0.0.2")
	keep := g.circuitBreakers.ForNamespaceGateway("beta", "127.0.0.1")

	rec := proxyOnce(g, "acme", httptest.NewRequest(http.MethodPost, "/v1/cache/get", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status %d, want 404 for a namespace with no gateway", rec.Code)
	}
	if g.circuitBreakers.Len() != 1 || g.circuitBreakers.ForNamespaceGateway("beta", "127.0.0.1") != keep {
		t.Fatalf("registry holds %d breakers, want only beta's", g.circuitBreakers.Len())
	}
}

// The reason a breaker keeps goes to the log and the node report; the client's
// error quotes the request URL, and a credential can be in its query.
func TestNamespaceProxy_aBreakersReasonCarriesNoCredentialFromTheURL(t *testing.T) {
	g := proxyGateway(t, gatewayTarget{ip: "127.0.0.1", port: freePort(t)})
	for i := 0; i < defaultFailureThreshold; i++ {
		proxyOnce(g, "acme", get("/v1/functions?api_key=SECRET-KEY-VALUE"))
	}
	got := g.circuitBreakers.Unhealthy(breakerReportIdle)
	if len(got) != 1 || !strings.Contains(got[0].LastError, "connection refused") {
		t.Fatalf("unhealthy = %+v, want one breaker whose reason is the refusal", got)
	}
	if strings.Contains(got[0].LastError, "SECRET-KEY-VALUE") || strings.Contains(got[0].LastError, "api_key") {
		t.Errorf("the breaker's reason quotes the request URL: %q", got[0].LastError)
	}
}
