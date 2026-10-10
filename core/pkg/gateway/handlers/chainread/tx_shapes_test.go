package chainread

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"
	"time"
)

// These tests pin the answers a wallet branches on, route by route, as docs/CHAIN.md states them.

func keysOf(t *testing.T, body []byte) string {
	t.Helper()
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(body, &doc); err != nil {
		t.Fatalf("not a JSON object: %v: %s", err, body)
	}
	keys := make([]string, 0, len(doc))
	for k := range doc {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return strings.Join(keys, ",")
}

func TestTxRoutes_aRefusedTransactionIs422AndJSONOnBothRoutes(t *testing.T) {
	log := "insufficient fees; got: 1norama required: 5norama"
	s := &rpcStub{
		simulate:  abciResult{Code: 13, Codespace: "sdk", Log: log},
		broadcast: map[string]any{"code": 13, "codespace": "sdk", "log": log, "hash": strings.Repeat("ab", 32)},
	}
	p := txProxy(t, s)
	cases := []struct {
		route, keys string
	}{
		{"simulate", "code,codespace,log"},
		{"broadcast", "code,codespace,log,tx_hash"},
	}
	for _, tc := range cases {
		rr := postTx(p, tc.route, "application/json", txBody(someTx))
		if rr.Code != http.StatusUnprocessableEntity {
			t.Errorf("%s: status %d, want 422: %s", tc.route, rr.Code, rr.Body)
		}
		if ct := rr.Header().Get("Content-Type"); ct != "application/json" {
			t.Errorf("%s: Content-Type %q", tc.route, ct)
		}
		if got := keysOf(t, rr.Body.Bytes()); got != tc.keys {
			t.Errorf("%s: refusal members %s, want %s", tc.route, got, tc.keys)
		}
		var doc struct {
			Code      uint32 `json:"code"`
			Codespace string `json:"codespace"`
			Log       string `json:"log"`
		}
		_ = json.Unmarshal(rr.Body.Bytes(), &doc)
		if doc.Code != 13 || doc.Codespace != "sdk" || doc.Log != log {
			t.Errorf("%s: refusal %+v", tc.route, doc)
		}
	}
}

func TestBroadcast_answerShapeAcceptedAndRepeated(t *testing.T) {
	hash := strings.Repeat("ab", 32)
	s := &rpcStub{broadcast: map[string]any{"code": 0, "codespace": "", "log": "", "hash": hash}}
	rr := postTx(txProxy(t, s), "broadcast", "application/json", txBody(someTx))
	if rr.Code != http.StatusOK || rr.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("status %d type %q: %s", rr.Code, rr.Header().Get("Content-Type"), rr.Body)
	}
	if got := keysOf(t, rr.Body.Bytes()); got != "code,codespace,log,tx_hash" {
		t.Errorf("accepted members %s", got)
	}
	if !strings.Contains(rr.Body.String(), `"tx_hash":"`+strings.ToUpper(hash)+`"`) || !strings.Contains(rr.Body.String(), `"code":0`) {
		t.Errorf("accepted answer %s", rr.Body)
	}

	s = &rpcStub{broadcastErr: map[string]any{"code": -32603, "message": "Internal error", "data": "tx already exists in cache"}}
	rr = postTx(txProxy(t, s), "broadcast", "application/json", txBody(someTx))
	if got := keysOf(t, rr.Body.Bytes()); rr.Code != http.StatusUnprocessableEntity || got != "code,codespace,log,tx_hash" ||
		!strings.Contains(rr.Body.String(), `"code":19`) || !strings.Contains(rr.Body.String(), `"log":"tx already in mempool cache"`) {
		t.Errorf("repeated transaction: status %d members %s: %s", rr.Code, got, rr.Body)
	}
}

func TestTxRoutes_aFailureThatIsNotARefusalIsPlainTextAndNeverARefusalShape(t *testing.T) {
	down, err := New(Config{RPCURL: "http://127.0.0.1:9", RESTURL: "http://127.0.0.1:9", IndexURL: "http://127.0.0.1:9"})
	if err != nil {
		t.Fatal(err)
	}
	release := make(chan struct{})
	hang := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		select {
		case <-r.Context().Done():
		case <-release:
		}
	}))
	t.Cleanup(hang.Close)
	t.Cleanup(func() { close(release) })
	slow, err := New(Config{RPCURL: hang.URL, RESTURL: hang.URL, IndexURL: hang.URL})
	if err != nil {
		t.Fatal(err)
	}
	slow.timeout = 50 * time.Millisecond
	full := txProxy(t, &rpcStub{broadcastErr: map[string]any{"code": -32603, "message": "Internal error", "data": "mempool is full"}})
	other := txProxy(t, &rpcStub{broadcastErr: map[string]any{"code": -32603, "message": "Internal error", "data": "boom"}})

	cases := []struct {
		name  string
		p     *Proxy
		route string
		code  int
		body  string
		retry string
	}{
		{"simulate, chain unreachable", down, "simulate", http.StatusBadGateway, "chain unreachable", ""},
		{"broadcast, chain unreachable", down, "broadcast", http.StatusBadGateway, "chain unreachable", ""},
		{"simulate, node past the timeout", slow, "simulate", http.StatusBadGateway, "chain unreachable", ""},
		{"broadcast, node past the timeout", slow, "broadcast", http.StatusBadGateway, "chain unreachable", ""},
		{"broadcast, mempool full", full, "broadcast", http.StatusServiceUnavailable, "the chain's mempool is full, try again shortly", txBusyRetryAfter},
		{"broadcast, any other node error", other, "broadcast", http.StatusBadGateway, msgChainFailure, ""},
	}
	for _, tc := range cases {
		rr := postTx(tc.p, tc.route, "application/json", txBody(someTx))
		if rr.Code != tc.code || strings.TrimSpace(rr.Body.String()) != tc.body || rr.Header().Get("Retry-After") != tc.retry {
			t.Errorf("%s: status %d retry-after %q body %q, want %d %q %q", tc.name, rr.Code, rr.Header().Get("Retry-After"), rr.Body.String(), tc.code, tc.retry, tc.body)
		}
		if ct := rr.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/plain") {
			t.Errorf("%s: Content-Type %q, want text/plain", tc.name, ct)
		}
		if json.Valid(rr.Body.Bytes()) {
			t.Errorf("%s: the answer parses as JSON, so a wallet could take it for a refusal: %s", tc.name, rr.Body)
		}
	}
}
