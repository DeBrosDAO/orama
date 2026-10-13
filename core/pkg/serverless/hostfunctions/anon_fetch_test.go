package hostfunctions

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"go.uber.org/zap"
)

// publicURLClient wraps a httptest server so fetches use a public hostname
// (denyInternalURL rejects loopback). The transport dials the test server.
func publicURLClient(srv *httptest.Server) (*http.Client, string) {
	publicURL := "https://example.com/fetch-test"
	inner := srv.Client().Transport
	return &http.Client{Transport: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		clone := req.Clone(req.Context())
		u, _ := url.Parse(srv.URL)
		clone.URL.Scheme = u.Scheme
		clone.URL.Host = u.Host
		return inner.RoundTrip(clone)
	})}, publicURL
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// feat-11 — AnonFetch (Tor-routed outbound HTTP for serverless fns,
// exported as anon_fetch and the deprecated alias anyone_fetch).
//
// The privacy contract is the part that matters: there must be NO silent
// fallback to the direct path when Tor is unavailable. A privacy
// regression has to fail loudly (error envelope), never degrade to a
// direct send that leaks the gateway↔upstream metadata trail the caller
// was trying to hide.

func TestAnonFetch_torDownIsAnErrorEnvelopeNotADirectSend(t *testing.T) {
	// The critical guarantee. With the Tor SOCKS port down, the anon
	// client's dial fails. AnonFetch MUST return {error, status:0} and
	// must never touch the direct client. If this regresses, every
	// wallet-RPC call AnChat routes through anyone_fetch would leak over
	// the gateway's direct egress without anybody noticing.
	directCalls := 0
	h := &HostFunctions{
		logger: zap.NewNop(),
		httpClient: &http.Client{Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
			directCalls++
			return nil, errors.New("direct client must not be used")
		})},
		anonHTTPClient: &http.Client{Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
			return nil, errors.New("dial through Tor SOCKS5 at 127.0.0.1:9050: connection refused")
		})},
	}

	raw, err := h.AnonFetch(context.Background(), "GET", "https://rpc.example.com", nil, nil)
	if err != nil {
		t.Fatalf("AnonFetch returned Go error; want an error envelope: %v", err)
	}
	var env map[string]interface{}
	if e := json.Unmarshal(raw, &env); e != nil {
		t.Fatalf("unmarshal envelope: %v", e)
	}
	if env["status"] != float64(0) {
		t.Errorf("status = %v; want 0 (transport failure marker)", env["status"])
	}
	if errStr, _ := env["error"].(string); !strings.Contains(errStr, "Tor") {
		t.Errorf("error = %q; want the Tor dial failure", errStr)
	}
	if _, hasBody := env["body"]; hasBody {
		t.Error("PRIVACY REGRESSION: envelope has a body although Tor was down")
	}
	if directCalls != 0 {
		t.Errorf("PRIVACY REGRESSION: the direct client was used %d times", directCalls)
	}
}

// The URL guard runs before any dial, on anon_fetch exactly as on http_fetch.
func TestAnonFetch_deniesLoopbackBeforeDialling(t *testing.T) {
	dialled := false
	h := &HostFunctions{
		logger: zap.NewNop(),
		anonHTTPClient: &http.Client{Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
			dialled = true
			return nil, errors.New("unreachable")
		})},
	}
	raw, err := h.AnonFetch(context.Background(), "GET", "http://127.0.0.1:10100/db/query", nil, nil)
	if err != nil {
		t.Fatalf("AnonFetch: %v", err)
	}
	var env map[string]interface{}
	if e := json.Unmarshal(raw, &env); e != nil {
		t.Fatal(e)
	}
	if env["status"] != float64(0) || env["error"] == nil {
		t.Errorf("envelope = %v; want a status-0 denial", env)
	}
	if dialled {
		t.Error("a loopback URL must be refused before the Tor client dials")
	}
}

func TestAnonFetch_routesThroughConfiguredClient(t *testing.T) {
	// AnonFetch uses the Tor client it was given (here a stand-in
	// pointing at a local test server — the SOCKS dialer is exercised by
	// the anonproxy package's own tests; here we verify
	// AnonFetch threads the request through whatever client it was
	// given and shapes the response envelope correctly).
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Test", "ok")
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","result":"0x1"}`))
	}))
	defer srv.Close()

	client, publicURL := publicURLClient(srv)
	h := &HostFunctions{
		logger:         zap.NewNop(),
		anonHTTPClient: client, // stand-in for the SOCKS-routed client
	}

	raw, err := h.AnonFetch(context.Background(), "POST", publicURL,
		map[string]string{"Content-Type": "application/json"},
		[]byte(`{"method":"getBalance"}`))
	if err != nil {
		t.Fatalf("AnonFetch: %v", err)
	}
	var env map[string]interface{}
	_ = json.Unmarshal(raw, &env)

	if env["status"] != float64(200) {
		t.Errorf("status = %v; want 200", env["status"])
	}
	body, _ := env["body"].(string)
	if body != `{"jsonrpc":"2.0","result":"0x1"}` {
		t.Errorf("body = %q; want the upstream JSON-RPC response", body)
	}
}

func TestAnonFetch_andHTTPFetch_shareEnvelopeShape(t *testing.T) {
	// Both fetch variants must produce the SAME envelope shape
	// (status/headers/body) so a function can swap http_fetch ↔
	// anon_fetch without changing its response parsing. Pin it by
	// running the same upstream through both and comparing keys.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("hello"))
	}))
	defer srv.Close()

	client, publicURL := publicURLClient(srv)
	h := &HostFunctions{
		logger:         zap.NewNop(),
		httpClient:     client,
		anonHTTPClient: client,
	}

	directRaw, _ := h.HTTPFetch(context.Background(), "GET", publicURL, nil, nil)
	anonRaw, _ := h.AnonFetch(context.Background(), "GET", publicURL, nil, nil)

	var d, a map[string]interface{}
	_ = json.Unmarshal(directRaw, &d)
	_ = json.Unmarshal(anonRaw, &a)

	for _, k := range []string{"status", "headers", "body"} {
		if _, ok := d[k]; !ok {
			t.Errorf("http_fetch envelope missing %q", k)
		}
		if _, ok := a[k]; !ok {
			t.Errorf("anon_fetch envelope missing %q (must match http_fetch shape)", k)
		}
	}
	if d["body"] != a["body"] || d["body"] != "hello" {
		t.Errorf("bodies differ: direct=%v anon=%v", d["body"], a["body"])
	}
}

func TestHTTPFetch_deniesLoopback(t *testing.T) {
	h := &HostFunctions{logger: zap.NewNop(), httpClient: http.DefaultClient}
	raw, err := h.HTTPFetch(context.Background(), "GET", "http://127.0.0.1:10100/db/query", nil, nil)
	if err != nil {
		t.Fatalf("HTTPFetch: %v", err)
	}
	var env map[string]interface{}
	if e := json.Unmarshal(raw, &env); e != nil {
		t.Fatal(e)
	}
	if env["status"] != float64(0) {
		t.Errorf("status = %v; want 0 (denied before dial)", env["status"])
	}
	msg, _ := env["error"].(string)
	if msg == "" {
		t.Error("denied fetch must return an error envelope")
	}
}
