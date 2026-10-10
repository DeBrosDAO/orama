package gateway

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/pkg/httputil"
	"github.com/DeBrosOfficial/network/pkg/logging"
)

// freePort returns a loopback port with nothing listening on it.
func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()
	return port
}

// proxyGateway is a gateway whose namespace "acme" has the member gateways
// targets, all on this host, which it treats as local: they are tried in port
// order.
func proxyGateway(t *testing.T, targets ...gatewayTarget) *Gateway {
	t.Helper()
	logger, err := logging.NewColoredLogger(logging.ComponentGeneral, false)
	if err != nil {
		t.Fatal(err)
	}
	g := &Gateway{
		logger:           logger,
		cfg:              &Config{},
		localWireGuardIP: "127.0.0.1",
		internalAuthKey:  []byte(strings.Repeat("k", 32)),
		mwCache:          newMiddlewareCache(time.Minute),
		circuitBreakers:  NewCircuitBreakerRegistry(),
		proxyTransport:   &http.Transport{},
	}
	g.mwCache.SetNamespaceTargets("acme", targets)
	return g
}

func serverPort(s *httptest.Server) int { return s.Listener.Addr().(*net.TCPAddr).Port }

// deadThenLive starts h on a loopback port above a port nothing listens on, and
// returns both: the namespace proxy tries the dead member first.
func deadThenLive(t *testing.T, h http.Handler) (dead int, live *httptest.Server) {
	t.Helper()
	a, b := freePort(t), freePort(t)
	if a == b {
		t.Fatal("the same free port was returned twice")
	}
	dead, livePort := min(a, b), max(a, b)
	ln, err := net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(livePort))
	if err != nil {
		t.Fatalf("cannot listen on the live member's port %d: %v", livePort, err)
	}
	live = httptest.NewUnstartedServer(h)
	live.Listener.Close()
	live.Listener = ln
	live.Start()
	t.Cleanup(live.Close)
	return dead, live
}

// A member whose gateway is restarting refuses the connection; the request,
// body included, goes to the next member instead of answering 503 (soak,
// stagenet 2026-10-04).
func TestNamespaceProxy_aRefusedMemberFailsOverToTheNext(t *testing.T) {
	got := make(chan string, 1)
	dead, upstream := deadThenLive(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		got <- string(b)
		w.WriteHeader(http.StatusCreated)
	}))
	g := proxyGateway(t, gatewayTarget{ip: "127.0.0.1", port: dead}, gatewayTarget{ip: "127.0.0.1", port: serverPort(upstream)})

	const body = `{"name":"fn"}`
	r := httptest.NewRequest(http.MethodPost, "/v1/functions", strings.NewReader(body))
	r.ContentLength = int64(len(body))
	rec := httptest.NewRecorder()
	g.proxyToNamespaceGateway(rec, r, "acme", namespaceProxyAuth{namespace: "acme"})

	if rec.Code != http.StatusCreated {
		t.Fatalf("status %d (%s), want the live member's 201", rec.Code, rec.Body.String())
	}
	if b := <-got; b != body {
		t.Fatalf("the live member received body %q, want %q", b, body)
	}
}

// A member that was reached and failed is not retried elsewhere: the request
// may have done its work there.
func TestNamespaceProxy_aReachedMemberIsNotRetried(t *testing.T) {
	var hits atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		hj, ok := w.(http.Hijacker)
		if !ok {
			t.Error("cannot hijack")
			return
		}
		conn, _, _ := hj.Hijack()
		conn.Close() // reached, then failed mid-request
	}))
	defer upstream.Close()
	g := proxyGateway(t, gatewayTarget{ip: "127.0.0.1", port: serverPort(upstream)})

	r := httptest.NewRequest(http.MethodPost, "/v1/functions", strings.NewReader("x"))
	rec := httptest.NewRecorder()
	g.proxyToNamespaceGateway(rec, r, "acme", namespaceProxyAuth{namespace: "acme"})
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status %d, want 503 for a member that failed mid-request", rec.Code)
	}
	if n := hits.Load(); n != 1 {
		t.Fatalf("the member was hit %d times, want 1", n)
	}
}

// A client that has gone away is not failed over, and its cancelled dials are
// not counted against the members' circuits.
func TestNamespaceProxy_aCancelledClientIsNotFailedOver(t *testing.T) {
	var hits atomic.Int32
	dead, upstream := deadThenLive(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits.Add(1) }))
	g := proxyGateway(t, gatewayTarget{ip: "127.0.0.1", port: dead}, gatewayTarget{ip: "127.0.0.1", port: serverPort(upstream)})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r := httptest.NewRequest(http.MethodGet, "/v1/functions", nil).WithContext(ctx)
	g.proxyToNamespaceGateway(httptest.NewRecorder(), r, "acme", namespaceProxyAuth{namespace: "acme"})
	if n := hits.Load(); n != 0 {
		t.Fatalf("a cancelled request reached a member %d times", n)
	}
	if !g.circuitBreakers.ForNamespaceGateway("acme", "127.0.0.1").Allow() {
		t.Fatal("a cancelled request opened the members' circuit")
	}
}

func TestIsDialFailure_onlyAFailedConnect(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close()
	_, dialErr := (&http.Client{}).Get("http://" + addr + "/")
	if !isDialFailure(dialErr) {
		t.Fatalf("a refused connection is not a dial failure: %v", dialErr)
	}
	if isDialFailure(io.ErrUnexpectedEOF) || isDialFailure(nil) {
		t.Fatal("a failure after connecting counted as a dial failure")
	}
}

// The proxy gives a caller it validated the long budget: a slow upload body
// reaches the namespace gateway in full instead of being cut off at the
// server's ReadTimeout and answered 504 (stagenet 2026-10-04).
func TestNamespaceProxy_aValidatedSlowUploadReachesTheMember(t *testing.T) {
	got := make(chan int, 1)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		got <- len(b)
	}))
	defer upstream.Close()
	g := proxyGateway(t, gatewayTarget{ip: "127.0.0.1", port: serverPort(upstream)})
	front := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		g.proxyToNamespaceGateway(w, r, "acme", namespaceProxyAuth{namespace: "acme"})
	}))
	front.Config.ReadTimeout = 200 * time.Millisecond
	front.Start()
	defer front.Close()

	if status, err := slowPost(t, front.URL+"/v1/storage/upload", 600*time.Millisecond, "head"); err != nil || status != http.StatusOK {
		t.Fatalf("a validated slow upload was cut off (%d, %v)", status, err)
	}
	if n := <-got; n != len("headtail") {
		t.Fatalf("the member received %d bytes, want %d", n, len("headtail"))
	}
}

// The stagenet demo page could not call its functions: the main gateway's CORS middleware set
// Access-Control-Allow-Origin, the namespace gateway set it too, and the proxy added the second to
// the first. A browser refuses two values. The namespace gateway's header replaces the proxy's.
func TestCopyProxiedHeaders_theUpstreamsHeaderReplacesTheProxysOwn(t *testing.T) {
	rec := httptest.NewRecorder()
	rec.Header().Set("Access-Control-Allow-Origin", "https://demo.example.org")
	rec.Header().Set("X-Proxy-Only", "kept")
	resp := &http.Response{Header: http.Header{}}
	resp.Header.Set("Access-Control-Allow-Origin", "https://demo.example.org")
	resp.Header.Add("Set-Cookie", "a=1")
	resp.Header.Add("Set-Cookie", "b=2")
	resp.Header.Set(httputil.HeaderTenantOrigin, "ns")

	copyProxiedHeaders(rec, resp)

	if got := rec.Header().Values("Access-Control-Allow-Origin"); len(got) != 1 {
		t.Errorf("Access-Control-Allow-Origin = %q, want one value", got)
	}
	if got := rec.Header().Values("Set-Cookie"); len(got) != 2 {
		t.Errorf("Set-Cookie = %q, want both of the upstream's", got)
	}
	if rec.Header().Get("X-Proxy-Only") != "kept" {
		t.Error("a header only the proxy set was dropped")
	}
	if rec.Header().Get(httputil.HeaderTenantOrigin) != "" {
		t.Error("the tenant-origin marker reached the client")
	}
}
