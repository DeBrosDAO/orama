package gateway

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// A failure through Tor must not tell the caller where the SOCKS port is or
// why the dial failed: that turns the proxy into a probe for whatever the exit
// can reach, which the tunnel already refuses to be.
func TestAnonProxyHandler_failureDoesNotEchoInternals(t *testing.T) {
	gw := newTestGateway(t)
	body := bytes.NewBufferString(`{"url":"https://unreachable.example.invalid/","method":"GET"}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/proxy/anon", body)
	w := httptest.NewRecorder()

	gw.anonProxyHandler(w, req)

	// Whether a Tor client happens to be running locally decides between 503
	// (no SOCKS port) and 502 (Tor could not reach the name).
	if w.Code != http.StatusServiceUnavailable && w.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502 or 503 (body: %s)", w.Code, w.Body.String())
	}
	got := strings.ToLower(w.Body.String())
	for _, leak := range []string{"127.0.0.1", "9050", "socks", "dial", "refused"} {
		if strings.Contains(got, leak) {
			t.Errorf("response leaks %q: %s", leak, w.Body.String())
		}
	}
}
