package tlsutil

import (
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// connCountingServer counts every TCP connection a client opens to it.
func connCountingServer(t *testing.T, status int) (*httptest.Server, *atomic.Int64) {
	t.Helper()
	var opened atomic.Int64
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(status)
		_, _ = io.WriteString(w, `{"ok":true}`)
	}))
	srv.Config.ConnState = func(_ net.Conn, s http.ConnState) {
		if s == http.StateNew {
			opened.Add(1)
		}
	}
	srv.Start()
	t.Cleanup(srv.Close)
	return srv, &opened
}

func getAndDrain(t *testing.T, c *http.Client, url string) {
	t.Helper()
	resp, err := c.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	if _, err := io.Copy(io.Discard, resp.Body); err != nil {
		t.Fatalf("drain body: %v", err)
	}
	if err := resp.Body.Close(); err != nil {
		t.Fatalf("close body: %v", err)
	}
}

// Bugboard 2729: callers build a client per request. Each one used to carry
// its own pool, so every request opened a connection that was then parked
// forever. Sequential requests through fresh clients must share one.
func TestNewHTTPClient_freshClientsReuseOneConnection(t *testing.T) {
	srv, opened := connCountingServer(t, http.StatusOK)

	const requests = 10
	for i := 0; i < requests; i++ {
		getAndDrain(t, NewHTTPClient(5*time.Second), srv.URL)
	}
	if got := opened.Load(); got != 1 {
		t.Fatalf("%d requests through fresh clients opened %d connections; want 1", requests, got)
	}
}

// Error statuses are where a body is most often left unread; the pool must
// still get the connection back.
func TestNewHTTPClient_reusesConnectionAfterErrorStatus(t *testing.T) {
	srv, opened := connCountingServer(t, http.StatusInternalServerError)

	for i := 0; i < 5; i++ {
		getAndDrain(t, NewHTTPClientForDomain(5*time.Second, "api.orama.network"), srv.URL)
	}
	if got := opened.Load(); got != 1 {
		t.Fatalf("5 requests answered 500 opened %d connections; want 1", got)
	}
}

func TestNewHTTPClient_sharesTransportUntilScopedRootsChange(t *testing.T) {
	resetScopedRoots(t)

	a := NewHTTPClient(time.Second).Transport
	b := NewHTTPClientForDomain(2*time.Second, "x.orama.network").Transport
	if a != b {
		t.Fatal("clients built with the same TLS settings got different transports")
	}
	if a.(*http.Transport).IdleConnTimeout <= 0 {
		t.Fatal("shared transport has no idle timeout; parked connections would never close")
	}

	ca := newPrivateCA(t, "ns-app.stagenet.example")
	if err := TrustCAForDomain("stagenet.example", ca.caFile); err != nil {
		t.Fatal(err)
	}
	c := NewHTTPClient(time.Second).Transport
	if c == a {
		t.Fatal("a scoped root was added but the client kept the transport built without it")
	}
	if c.(*http.Transport).TLSClientConfig.VerifyConnection == nil {
		t.Fatal("rebuilt transport does not verify against the scoped roots")
	}
}

// The client-level Timeout stays per caller even though the pool is shared.
func TestNewHTTPClient_zeroTimeoutKeepsNoClientDeadline(t *testing.T) {
	if got := NewHTTPClient(0).Timeout; got != 0 {
		t.Fatalf("NewHTTPClient(0).Timeout = %v; want 0", got)
	}
	if got := NewHTTPClient(3 * time.Second).Timeout; got != 3*time.Second {
		t.Fatalf("NewHTTPClient(3s).Timeout = %v; want 3s", got)
	}
}
