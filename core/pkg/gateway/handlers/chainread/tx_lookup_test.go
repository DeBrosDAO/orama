package chainread

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// CometBFT's URI handler answers every error from a route with HTTP 500, a transaction that is not
// in its index yet included: a wallet that polls /v1/chain/tx right after broadcasting must get a
// 404 to retry, not a 502 (bug #739).
func TestProxy_aTransactionNotFoundYetIs404ToRetry(t *testing.T) {
	hash := strings.Repeat("ab", 32)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":-1,"error":{"code":-32603,"message":"Internal error","data":"tx (` + strings.ToUpper(hash) + `) not found"}}`))
	}))
	t.Cleanup(upstream.Close)
	p := mustProxy(t, upstream.URL, upstream.URL)
	rec := httptest.NewRecorder()
	p.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/chain/tx?hash="+hash, nil))
	if rec.Code != http.StatusNotFound || strings.TrimSpace(rec.Body.String()) != "not found on chain" {
		t.Fatalf("status %d body %q, want 404 not found on chain", rec.Code, rec.Body.String())
	}
	if rec.Header().Get("Retry-After") != txNotFoundRetryAfter {
		t.Errorf("Retry-After %q", rec.Header().Get("Retry-After"))
	}
}

// A 500 that is not a "not found" is still a bad gateway, and a 404 elsewhere carries no Retry-After.
func TestProxy_otherRPCErrorsStayBadGatewayAndOnlyATxNotFoundSaysRetry(t *testing.T) {
	body := `{"jsonrpc":"2.0","id":-1,"error":{"code":-32603,"message":"Internal error","data":"tx index disabled at /data/idx"}}`
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(upstream.Close)
	p := mustProxy(t, upstream.URL, upstream.URL)
	rec := httptest.NewRecorder()
	p.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/chain/tx?hash="+strings.Repeat("cd", 32), nil))
	if rec.Code != http.StatusBadGateway || strings.Contains(rec.Body.String(), "/data") {
		t.Fatalf("status %d body %q", rec.Code, rec.Body.String())
	}
	body = `{"jsonrpc":"2.0","id":-1,"error":{"code":-32603,"message":"Internal error","data":"block 9 not found"}}`
	rec = httptest.NewRecorder()
	p.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/chain/block?height=9", nil))
	if rec.Code != http.StatusNotFound || rec.Header().Get("Retry-After") != "" {
		t.Fatalf("status %d retry-after %q", rec.Code, rec.Header().Get("Retry-After"))
	}
}
