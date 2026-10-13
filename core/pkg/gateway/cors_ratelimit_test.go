package gateway

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// limitedGateway is a gateway whose general bucket holds one request, run through the chain the server
// actually runs (withMiddleware), so the order of the rate limiter and CORS is what is tested.
func limitedGateway(t *testing.T) (http.Handler, *bool) {
	t.Helper()
	g := newReadinessGateway(t)
	g.cfg = &Config{BaseDomain: "stagenet.orama.network"}
	g.ready.set(ReadinessReady, "", "")
	g.rateLimiter = NewRateLimiter(1, 1)
	reached := false
	return g.withMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached = true
		w.WriteHeader(http.StatusOK)
	})), &reached
}

// chainGet makes a GET of the public chain proxy from a client address and an origin.
func chainGet(h http.Handler, origin string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, "/v1/chain/status", nil)
	req.RemoteAddr = "203.0.113.9:4444"
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestRateLimit_a429ToTheExplorerIsReadableByTheExplorersOrigin(t *testing.T) {
	h, _ := limitedGateway(t)
	if rec := chainGet(h, publicExplorerOrigin); rec.Code != http.StatusOK {
		t.Fatalf("first read: HTTP %d, want 200", rec.Code)
	}
	rec := chainGet(h, publicExplorerOrigin)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("second read: HTTP %d, want 429", rec.Code)
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != publicExplorerOrigin {
		t.Errorf("the 429 has Access-Control-Allow-Origin %q, want %q: a browser on orama.network cannot read it", got, publicExplorerOrigin)
	}
	if rec.Header().Get("Vary") != "Origin" {
		t.Errorf("Vary %q, want Origin", rec.Header().Get("Vary"))
	}
	if rec.Header().Get("Retry-After") == "" {
		t.Error("the 429 has no Retry-After")
	}
	if rec.Header().Get("Access-Control-Allow-Credentials") != "" {
		t.Error("the 429 allows credentials")
	}
}

func TestRateLimit_a429IsReadableByAnAppUnderTheBaseDomain(t *testing.T) {
	h, _ := limitedGateway(t)
	const app = "https://myapp.stagenet.orama.network"
	chainGet(h, app)
	rec := chainGet(h, app)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("HTTP %d, want 429", rec.Code)
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != app {
		t.Errorf("Access-Control-Allow-Origin %q, want %q", got, app)
	}
}

// CORS has not become broader: an origin the gateway never allowed is told the gateway's own origin on
// a 429 as on every other answer, which a browser refuses to hand to that page.
func TestRateLimit_a429ToADisallowedOriginIsNotReadableByIt(t *testing.T) {
	h, _ := limitedGateway(t)
	const other = "https://evil.example"
	chainGet(h, other)
	rec := chainGet(h, other)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("HTTP %d, want 429", rec.Code)
	}
	got := rec.Header().Get("Access-Control-Allow-Origin")
	if got == other || got == "*" {
		t.Errorf("Access-Control-Allow-Origin %q lets %s read the 429", got, other)
	}
	if got != "https://stagenet.orama.network" {
		t.Errorf("Access-Control-Allow-Origin %q, want the gateway's own origin as on every other answer", got)
	}
}

// The explorer's exception is for a read of the chain proxy only: the same origin asking for anything
// else is not let in, on a 429 either.
func TestRateLimit_theExplorerOriginGetsNothingOffTheChainReadRoutes(t *testing.T) {
	g := newReadinessGateway(t)
	g.cfg = &Config{BaseDomain: "stagenet.orama.network"}
	g.ready.set(ReadinessReady, "", "")
	g.rateLimiter = NewRateLimiter(1, 1)
	h := g.withMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) }))
	get := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "/v1/health", nil)
		req.RemoteAddr = "203.0.113.9:4444"
		req.Header.Set("Origin", publicExplorerOrigin)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}
	get()
	rec := get()
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("HTTP %d, want 429", rec.Code)
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got == publicExplorerOrigin {
		t.Errorf("the explorer's origin was let in off the chain proxy: %q", got)
	}
}

// A preflight is answered before the limiter, with the same headers as before.
func TestRateLimit_aPreflightOfAChainReadIsStillAnswered(t *testing.T) {
	h, reached := limitedGateway(t)
	req := httptest.NewRequest(http.MethodOptions, "/v1/chain/status", nil)
	req.RemoteAddr = "203.0.113.9:4444"
	req.Header.Set("Origin", publicExplorerOrigin)
	req.Header.Set("Access-Control-Request-Method", http.MethodGet)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("HTTP %d, want 204", rec.Code)
	}
	if rec.Header().Get("Access-Control-Allow-Origin") != publicExplorerOrigin {
		t.Errorf("Allow-Origin %q", rec.Header().Get("Access-Control-Allow-Origin"))
	}
	if *reached {
		t.Error("a preflight reached the handler")
	}
}

// A preflight is answered before the limiter: one sent after the client's bucket is spent is still
// a 204 with the CORS headers, and preflights spend nothing, so a GET after one still gets through.
func TestRateLimit_preflightsNeitherNeedNorSpendTheBucket(t *testing.T) {
	preflight := func(h http.Handler) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodOptions, "/v1/chain/status", nil)
		req.RemoteAddr = "203.0.113.9:4444"
		req.Header.Set("Origin", publicExplorerOrigin)
		req.Header.Set("Access-Control-Request-Method", http.MethodGet)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}

	spent, _ := limitedGateway(t)
	chainGet(spent, publicExplorerOrigin)
	if rec := chainGet(spent, publicExplorerOrigin); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("the bucket is not spent after two GETs: HTTP %d", rec.Code)
	}
	if rec := preflight(spent); rec.Code != http.StatusNoContent || rec.Header().Get("Access-Control-Allow-Origin") != publicExplorerOrigin {
		t.Fatalf("a preflight after the bucket was spent: HTTP %d, Allow-Origin %q", rec.Code, rec.Header().Get("Access-Control-Allow-Origin"))
	}

	fresh, _ := limitedGateway(t)
	for i := 0; i < 5; i++ {
		preflight(fresh)
	}
	if rec := chainGet(fresh, publicExplorerOrigin); rec.Code == http.StatusTooManyRequests {
		t.Fatal("preflights spent the bucket the GET needed")
	}
}
