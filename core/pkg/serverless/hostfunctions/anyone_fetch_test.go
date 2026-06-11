package hostfunctions

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"go.uber.org/zap"
)

// feat-11 — AnyoneFetch (Anyone-routed outbound HTTP for serverless fns).
//
// The privacy contract is the part that matters: there must be NO silent
// fallback to the direct path when Anyone routing is unavailable. A
// privacy regression has to fail loudly (typed error), never degrade to
// a direct send that leaks the gateway↔upstream metadata trail the
// caller was trying to hide.

func TestAnyoneFetch_nilClientReturnsTypedErrorNotDirectSend(t *testing.T) {
	// The critical guarantee. When Anyone routing is disabled on this
	// gateway, anyoneHTTPClient is nil. AnyoneFetch MUST return the
	// typed {error, status:0, proxy:"anyone"} envelope — NOT silently
	// dial direct. If this regresses, every wallet-RPC call AnChat
	// routes through anyone_fetch would leak over the gateway's direct
	// egress without anyone noticing.
	h := &HostFunctions{
		logger: zap.NewNop(),
		// anyoneHTTPClient intentionally nil (Anyone disabled)
	}

	raw, err := h.AnyoneFetch(context.Background(), "GET", "https://rpc.example.com", nil, nil)
	if err != nil {
		t.Fatalf("AnyoneFetch returned Go error; want typed envelope: %v", err)
	}
	var env map[string]interface{}
	if e := json.Unmarshal(raw, &env); e != nil {
		t.Fatalf("unmarshal envelope: %v", e)
	}
	if env["status"] != float64(0) {
		t.Errorf("status = %v; want 0 (transport/setup failure marker)", env["status"])
	}
	if env["proxy"] != "anyone" {
		t.Errorf("proxy = %v; want \"anyone\" (so caller can distinguish anyone-path failure)", env["proxy"])
	}
	errStr, _ := env["error"].(string)
	if errStr == "" {
		t.Error("error field empty; want an actionable 'anyone routing not available' message")
	}
	// The envelope must NOT contain a body — a nil client means we never
	// made a request, so there's no upstream response. Presence of a
	// body here would imply a direct send happened.
	if _, hasBody := env["body"]; hasBody {
		t.Error("PRIVACY REGRESSION: envelope has a body — a request was made despite nil anyone client (silent direct fallback?)")
	}
}

func TestAnyoneFetch_routesThroughConfiguredClient(t *testing.T) {
	// When an Anyone client IS configured, AnyoneFetch uses it (here a
	// stand-in pointing at a local test server — the SOCKS dialer is
	// exercised by the anyoneproxy package's own tests; here we verify
	// AnyoneFetch threads the request through whatever client it was
	// given and shapes the response envelope correctly).
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Test", "ok")
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","result":"0x1"}`))
	}))
	defer srv.Close()

	h := &HostFunctions{
		logger:           zap.NewNop(),
		anyoneHTTPClient: srv.Client(), // stand-in for the SOCKS-routed client
	}

	raw, err := h.AnyoneFetch(context.Background(), "POST", srv.URL,
		map[string]string{"Content-Type": "application/json"},
		[]byte(`{"method":"getBalance"}`))
	if err != nil {
		t.Fatalf("AnyoneFetch: %v", err)
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

func TestAnyoneFetch_andHTTPFetch_shareEnvelopeShape(t *testing.T) {
	// Both fetch variants must produce the SAME envelope shape
	// (status/headers/body) so a function can swap http_fetch ↔
	// anyone_fetch without changing its response parsing. Pin it by
	// running the same upstream through both and comparing keys.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("hello"))
	}))
	defer srv.Close()

	h := &HostFunctions{
		logger:           zap.NewNop(),
		httpClient:       srv.Client(),
		anyoneHTTPClient: srv.Client(),
	}

	directRaw, _ := h.HTTPFetch(context.Background(), "GET", srv.URL, nil, nil)
	anyoneRaw, _ := h.AnyoneFetch(context.Background(), "GET", srv.URL, nil, nil)

	var d, a map[string]interface{}
	_ = json.Unmarshal(directRaw, &d)
	_ = json.Unmarshal(anyoneRaw, &a)

	for _, k := range []string{"status", "headers", "body"} {
		if _, ok := d[k]; !ok {
			t.Errorf("http_fetch envelope missing %q", k)
		}
		if _, ok := a[k]; !ok {
			t.Errorf("anyone_fetch envelope missing %q (must match http_fetch shape)", k)
		}
	}
	if d["body"] != a["body"] || d["body"] != "hello" {
		t.Errorf("bodies differ: direct=%v anyone=%v", d["body"], a["body"])
	}
}
