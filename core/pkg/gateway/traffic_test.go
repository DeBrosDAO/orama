package gateway

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/logging"
	"github.com/DeBrosOfficial/network/pkg/telemetry/traffic"
)

func newTrafficTestGateway(t *testing.T, clientNamespace string) *Gateway {
	t.Helper()
	logger, err := logging.NewColoredLogger(logging.ComponentGeneral, false)
	if err != nil {
		t.Fatalf("failed to create logger: %v", err)
	}
	return &Gateway{
		logger:  logger,
		cfg:     &Config{ClientNamespace: clientNamespace},
		traffic: traffic.New(nil),
	}
}

// trafficResolveNamespace stands in for authMiddleware: it resolves the namespace into
// the request context and runs the rest of the chain.
func trafficResolveNamespace(ns string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), CtxKeyNamespaceOverride, ns)))
	})
}

func trafficRespond(status int) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(status)
		_, _ = w.Write([]byte("body"))
	})
}

func serveTraffic(h http.Handler, r *http.Request) {
	h.ServeHTTP(httptest.NewRecorder(), r)
}

func onlyTrafficNamespace(t *testing.T, g *Gateway) (string, int64, int64) {
	t.Helper()
	snap := g.TrafficSnapshot()
	if snap == nil || len(snap.Namespaces) != 1 {
		t.Fatalf("snapshot = %+v, want exactly one namespace", snap)
	}
	ns := snap.Namespaces[0]
	return ns.Namespace, ns.Requests, ns.Errors5xx
}

func TestLoggingMiddleware_recordsResolvedNamespace(t *testing.T) {
	g := newTrafficTestGateway(t, "index")
	h := g.loggingMiddleware(trafficResolveNamespace("anchat", g.trafficAttributionMiddleware(trafficRespond(http.StatusServiceUnavailable))))
	serveTraffic(h, httptest.NewRequest(http.MethodGet, "/v1/storage/get", nil))

	ns, reqs, errs := onlyTrafficNamespace(t, g)
	if ns != "anchat" || reqs != 1 || errs != 1 {
		t.Fatalf("got %s %d req %d 5xx, want anchat 1 req 1 5xx", ns, reqs, errs)
	}
	if snap := g.TrafficSnapshot(); snap.Errors5xx != 1 || snap.ErrorRate != 1 {
		t.Fatalf("snapshot = %+v, want one 5xx and error rate 1", snap)
	}
}

func TestLoggingMiddleware_unattributedUsesClientNamespace(t *testing.T) {
	g := newTrafficTestGateway(t, "index")
	serveTraffic(g.loggingMiddleware(trafficRespond(http.StatusUnauthorized)), httptest.NewRequest(http.MethodGet, "/v1/storage/get", nil))

	ns, reqs, _ := onlyTrafficNamespace(t, g)
	if ns != "index" || reqs != 1 {
		t.Fatalf("got %s %d req, want index 1 req", ns, reqs)
	}
	if snap := g.TrafficSnapshot(); snap.Errors4xx != 1 {
		t.Fatalf("4xx = %d, want 1", snap.Errors4xx)
	}
}

func TestLoggingMiddleware_excludesHealthAndTelemetry(t *testing.T) {
	g := newTrafficTestGateway(t, "index")
	h := g.loggingMiddleware(trafficRespond(http.StatusOK))
	for _, p := range []string{"/health", "/v1/health", "/v1/internal/ping", "/v1/internal/telemetry", "/v1/internal/telemetry/node", "/v1/operator/telemetry/cluster"} {
		serveTraffic(h, httptest.NewRequest(http.MethodGet, p, nil))
	}
	if snap := g.TrafficSnapshot(); snap.Requests != 0 || snap.TotalRequests != 0 {
		t.Fatalf("snapshot = %+v, want nothing recorded", snap)
	}
}

func TestTrafficExcluded_cases(t *testing.T) {
	cases := map[string]bool{
		"/health":                  true,
		"/v1/health":               true,
		"/v1/internal/ping":        true,
		"/v1/internal/telemetry/x": true,
		"/v1/operator/telemetry":   true,
		"/healthz":                 false,
		"/v1/health/deep":          false,
		"/v1/internal/pingx":       false,
		"/v1/operator/namespaces":  false,
		"":                         false,
		"/v1/storage/upload":       false,
		"/v1/internal/telemetryx":  false,
	}
	for path, want := range cases {
		if got := trafficExcluded(path); got != want {
			t.Errorf("trafficExcluded(%q) = %v, want %v", path, got, want)
		}
	}
}

func TestRecordTraffic_webSocketHasNoLatencySample(t *testing.T) {
	g := newTrafficTestGateway(t, "index")
	r := httptest.NewRequest(http.MethodGet, "/v1/pubsub/ws", nil)
	r.Header.Set("Connection", "Upgrade")
	r.Header.Set("Upgrade", "websocket")
	serveTraffic(g.loggingMiddleware(trafficRespond(http.StatusSwitchingProtocols)), r)

	snap := g.TrafficSnapshot()
	if snap.Requests != 1 || snap.P99Ms != 0 {
		t.Fatalf("snapshot = %+v, want 1 request without a latency sample", snap)
	}
}

func TestProxyToNamespaceGateway_attributesTargetNamespace(t *testing.T) {
	g := newTrafficTestGateway(t, "index")
	h := g.loggingMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		g.proxyToNamespaceGateway(w, r, "anchat", namespaceProxyAuth{errMsg: "invalid API key"})
	}))
	serveTraffic(h, httptest.NewRequest(http.MethodGet, "/v1/storage/get", nil))

	ns, reqs, _ := onlyTrafficNamespace(t, g)
	if ns != "anchat" || reqs != 1 {
		t.Fatalf("got %s %d req, want the refused request attributed to anchat", ns, reqs)
	}
}

func TestMarkTrafficNamespace_noSlotOrEmptyIsNoop(t *testing.T) {
	markTrafficNamespace(httptest.NewRequest(http.MethodGet, "/", nil), "anchat")

	r, a := withTrafficAttribution(httptest.NewRequest(http.MethodGet, "/", nil))
	markTrafficNamespace(r, "anchat")
	markTrafficNamespace(r, "")
	if a.namespace != "anchat" {
		t.Fatalf("namespace = %q, want anchat kept after an empty mark", a.namespace)
	}
}

func TestTrafficSnapshot_nilRecorder(t *testing.T) {
	g := &Gateway{}
	if snap := g.TrafficSnapshot(); snap != nil {
		t.Fatalf("snapshot = %+v, want nil", snap)
	}
	g.recordTraffic(httptest.NewRequest(http.MethodGet, "/", nil), &trafficAttribution{}, http.StatusOK, 0, 0)
}
