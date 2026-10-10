package gateway

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

const (
	proxiedHost = "hidden-destination.example"
	proxiedURL  = "https://" + proxiedHost + "/private/path?token=" + leakedCredential
)

// usesTorStub makes the handler think Tor is up and sends its request through
// rt, and restores the real ones afterwards.
func usesTorStub(t *testing.T, rt http.RoundTripper) {
	t.Helper()
	prevRunning, prevClient := anonProxyRunning, anonProxyClient
	anonProxyRunning = func() bool { return true }
	anonProxyClient = func() *http.Client { return &http.Client{Transport: rt} }
	t.Cleanup(func() { anonProxyRunning, anonProxyClient = prevRunning, prevClient })
}

func anonProxyCall(g *Gateway) *httptest.ResponseRecorder {
	body := bytes.NewBufferString(`{"url":"` + proxiedURL + `","method":"GET"}`)
	w := httptest.NewRecorder()
	g.anonProxyHandler(w, httptest.NewRequest(http.MethodPost, "/v1/proxy/anon", body))
	return w
}

// The anonymity proxy exists so that the node cannot say who read what: its
// log must not name the host, path or query it was asked to fetch, on success
// or on failure.
func assertNoDestination(t *testing.T, lines []string) {
	t.Helper()
	if len(lines) == 0 {
		t.Fatal("nothing was logged, so the test checks nothing")
	}
	for _, l := range lines {
		for _, leaked := range []string{proxiedHost, "/private/path", "token=", leakedCredential, "https://"} {
			if strings.Contains(l, leaked) {
				t.Errorf("a log line names the destination (%q): %s", leaked, l)
			}
		}
	}
}

func TestAnonProxyHandler_successLogNamesNoDestination(t *testing.T) {
	usesTorStub(t, roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader("ok")), Request: r}, nil
	}))
	g := newTestGateway(t)
	lines := observed(g)

	if w := anonProxyCall(g); w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	logged := lines()
	assertNoDestination(t, logged)
	joined := strings.Join(logged, "\n")
	for _, kept := range []string{"status:200", "bytes:2", "duration"} {
		if !strings.Contains(joined, kept) {
			t.Errorf("the completion line lost %q: %s", kept, joined)
		}
	}
}

func TestAnonProxyHandler_failureLogNamesNoDestination(t *testing.T) {
	usesTorStub(t, roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return nil, errors.New("socks connect tcp 127.0.0.1:9050->" + proxiedHost + ":443: host unreachable")
	}))
	g := newTestGateway(t)
	lines := observed(g)

	if w := anonProxyCall(g); w.Code != http.StatusBadGateway {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	logged := lines()
	assertNoDestination(t, logged)
	if !strings.Contains(strings.Join(logged, "\n"), "error_class:transport") {
		t.Errorf("the failure line lost its error class: %v", logged)
	}
}

func TestAnonProxyHandler_readFailureLogNamesNoDestination(t *testing.T) {
	usesTorStub(t, roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: http.Header{}, Request: r,
			Body: io.NopCloser(&failingReader{err: errors.New("read " + proxiedHost + ": reset")})}, nil
	}))
	g := newTestGateway(t)
	lines := observed(g)

	if w := anonProxyCall(g); w.Code != http.StatusBadGateway {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	assertNoDestination(t, lines())
}

type failingReader struct{ err error }

func (f *failingReader) Read([]byte) (int, error) { return 0, f.err }
