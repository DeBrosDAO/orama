package chainread

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

func TestConfigFromEnv_loopbackDefaults(t *testing.T) {
	t.Setenv("ORAMA_CHAIN_RPC_URL", "")
	t.Setenv("ORAMA_CHAIN_REST_URL", "   ")
	t.Setenv("ORAMA_CHAIN_INDEX_URL", "")
	cfg := ConfigFromEnv()
	if cfg.IndexURL != "http://127.0.0.1:31015" {
		t.Errorf("index = %q, want http://127.0.0.1:31015", cfg.IndexURL)
	}
	if cfg.RPCURL != "http://127.0.0.1:31001" {
		t.Errorf("RPC = %q, want http://127.0.0.1:31001", cfg.RPCURL)
	}
	if cfg.RESTURL != "http://127.0.0.1:31003" {
		t.Errorf("REST = %q, want http://127.0.0.1:31003", cfg.RESTURL)
	}
}

func TestConfigFromEnv_usesSetURLs(t *testing.T) {
	t.Setenv("ORAMA_CHAIN_RPC_URL", "http://10.0.0.2:31001")
	t.Setenv("ORAMA_CHAIN_REST_URL", "http://10.0.0.2:31003/")
	t.Setenv("ORAMA_CHAIN_INDEX_URL", "http://10.0.0.2:31015")
	cfg := ConfigFromEnv()
	if cfg.RPCURL != "http://10.0.0.2:31001" || cfg.RESTURL != "http://10.0.0.2:31003/" || cfg.IndexURL != "http://10.0.0.2:31015" {
		t.Fatalf("config = %+v", cfg)
	}
}

func TestNew_rejectsNonBaseURLs(t *testing.T) {
	bad := []string{
		"",
		"not-a-url",
		"ftp://127.0.0.1:31001",
		"http://user:pass@127.0.0.1:31001",
		"http://127.0.0.1:31001/status",
		"http://127.0.0.1:31001?x=1",
		"http://127.0.0.1:31001#frag",
	}
	ok := "http://127.0.0.1:31003"
	for _, raw := range bad {
		if _, err := New(Config{RPCURL: raw, RESTURL: ok, IndexURL: ok}); err == nil {
			t.Errorf("RPC %q was accepted", raw)
		}
		if _, err := New(Config{RPCURL: ok, RESTURL: raw, IndexURL: ok}); err == nil {
			t.Errorf("REST %q was accepted", raw)
		}
		if _, err := New(Config{RPCURL: ok, RESTURL: ok, IndexURL: raw}); err == nil {
			t.Errorf("index %q was accepted", raw)
		}
	}
	if _, err := New(Config{RPCURL: "http://127.0.0.1:31001/", RESTURL: ok, IndexURL: ok}); err != nil {
		t.Errorf("trailing slash on the base URL was refused: %v", err)
	}
}

func TestProxy_statusBodyUnchanged(t *testing.T) {
	body := []byte("{\n  \"jsonrpc\": \"2.0\",\n  \"id\": -1,\n  \"result\": {\"node_info\":{\"network\":\"orama-stagenet-1\"}}\n}\n")
	const ct = "application/json; charset=utf-8"
	var gotHost, gotCookie, gotAuth, gotURI string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHost = r.Host
		gotCookie = r.Header.Get("Cookie")
		gotAuth = r.Header.Get("Authorization")
		gotURI = r.RequestURI
		if r.Method != http.MethodGet {
			t.Errorf("method %s", r.Method)
		}
		w.Header().Set("Content-Type", ct)
		w.Header().Set("Set-Cookie", "secret=1")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(body)
	}))
	t.Cleanup(upstream.Close)

	p := mustProxy(t, upstream.URL, "http://127.0.0.1:9")
	req := httptest.NewRequest(http.MethodGet, "/v1/chain/status", nil)
	req.Host = "example.com"
	req.Header.Set("Cookie", "sid=secret")
	req.Header.Set("Authorization", "Bearer secret")
	rec := httptest.NewRecorder()
	p.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status %d, body %s", rec.Code, rec.Body.Bytes())
	}
	if !bytes.Equal(rec.Body.Bytes(), body) {
		t.Fatalf("body = %q, want %q", rec.Body.Bytes(), body)
	}
	if rec.Header().Get("Content-Type") != ct {
		t.Errorf("content-type %q", rec.Header().Get("Content-Type"))
	}
	if rec.Header().Get("Set-Cookie") != "" {
		t.Errorf("forwarded Set-Cookie %q", rec.Header().Get("Set-Cookie"))
	}
	if gotURI != "/status" {
		t.Errorf("upstream URI %q", gotURI)
	}
	if gotCookie != "" || gotAuth != "" {
		t.Errorf("forwarded caller credentials cookie=%q auth=%q", gotCookie, gotAuth)
	}
	if gotHost == "example.com" || gotHost == "" {
		t.Errorf("upstream host %q", gotHost)
	}
}

func TestProxy_disallowedPathNotFetched(t *testing.T) {
	var hits int
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(upstream.Close)
	p := mustProxy(t, upstream.URL, upstream.URL)

	paths := []string{
		"/v1/chain/broadcast_tx_commit",
		"/v1/chain/abci_query",
		"/v1/chain/status/extra",
		"/v1/chain/cosmos/bank/v1beta1/supply/by_denom",
		"/v1/chain/supply/uatom",
		"/v1/chain/block/1",
		"/v1/chain/tx/ABCDEF",
		"/v1/chain/status/",
		"/v1/chain//status",
	}
	for _, path := range paths {
		rec := httptest.NewRecorder()
		p.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusNotFound {
			t.Errorf("%s: status %d, want 404 (%s)", path, rec.Code, rec.Body.String())
		}
	}

	dot := httptest.NewRequest(http.MethodGet, "/v1/chain/status", nil)
	dot.URL.Path = "/v1/chain/../v1/chain/status"
	rec := httptest.NewRecorder()
	p.ServeHTTP(rec, dot)
	if rec.Code != http.StatusNotFound {
		t.Errorf("dotdot: status %d", rec.Code)
	}

	encoded := httptest.NewRequest(http.MethodGet, "/v1/chain/status", nil)
	encoded.URL.RawPath = "/v1/chain/%2e%2e/status"
	encRec := httptest.NewRecorder()
	p.ServeHTTP(encRec, encoded)
	if encRec.Code != http.StatusNotFound {
		t.Errorf("encoded dot: status %d", encRec.Code)
	}

	queries := []string{
		"/v1/chain/status?x=1",
		"/v1/chain/block?height=0",
		"/v1/chain/block?height=01",
		"/v1/chain/block?height=12&extra=1",
		"/v1/chain/blocks?min_height=1&max_height=100",
		"/v1/chain/tx?hash=abc",
		"/v1/chain/tx?hash=" + strings.Repeat("ab", 32) + "&prove=true",
		"/v1/chain/supply/norama?denom=uatom",
		"/v1/chain/staking/pool?x=1",
		"/v1/chain/validators?page=1&per_page=101",
	}
	for _, path := range queries {
		rec := httptest.NewRecorder()
		p.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s: status %d, want 400", path, rec.Code)
		}
	}

	post := httptest.NewRecorder()
	p.ServeHTTP(post, httptest.NewRequest(http.MethodPost, "/v1/chain/status", nil))
	if post.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST status %d", post.Code)
	}
	if post.Header().Get("Allow") != http.MethodGet {
		t.Errorf("Allow %q", post.Header().Get("Allow"))
	}

	if hits != 0 {
		t.Fatalf("upstream was called %d times", hits)
	}
}

func TestProxy_allowlistHitsTheMatchingUpstream(t *testing.T) {
	hash := strings.Repeat("ab", 32)
	type hit struct {
		rpc bool
		uri string
	}
	want := []struct {
		path string
		hit  hit
	}{
		{"/v1/chain/status", hit{true, "/status"}},
		{"/v1/chain/block?height=12", hit{true, "/block?height=12"}},
		{"/v1/chain/blocks?min_height=1&max_height=8", hit{true, "/blockchain?maxHeight=8&minHeight=1"}},
		{"/v1/chain/tx?hash=0x" + strings.ToUpper(hash), hit{true, "/tx?hash=0x" + hash}},
		{"/v1/chain/validators", hit{true, "/validators?page=1&per_page=100"}},
		{"/v1/chain/validators?page=2&per_page=30", hit{true, "/validators?page=2&per_page=30"}},
		{"/v1/chain/supply/norama", hit{false, "/cosmos/bank/v1beta1/supply/by_denom?denom=norama"}},
		{"/v1/chain/staking/pool", hit{false, "/cosmos/staking/v1beta1/pool"}},
	}

	var mu sync.Mutex
	var rpcHits, restHits []string
	record := func(dst *[]string) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			*dst = append(*dst, r.RequestURI)
			mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = io.WriteString(w, "{}")
		})
	}
	rpc := httptest.NewServer(record(&rpcHits))
	rest := httptest.NewServer(record(&restHits))
	t.Cleanup(rpc.Close)
	t.Cleanup(rest.Close)
	p := mustProxy(t, rpc.URL, rest.URL)

	for _, tc := range want {
		rpcHits, restHits = nil, nil
		rec := httptest.NewRecorder()
		p.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, tc.path, nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: status %d (%s)", tc.path, rec.Code, rec.Body.String())
		}
		gotRPC, gotREST := append([]string(nil), rpcHits...), append([]string(nil), restHits...)
		if tc.hit.rpc {
			if len(gotREST) != 0 || len(gotRPC) != 1 || gotRPC[0] != tc.hit.uri {
				t.Errorf("%s: rpc %v rest %v, want rpc %s", tc.path, gotRPC, gotREST, tc.hit.uri)
			}
		} else if len(gotRPC) != 0 || len(gotREST) != 1 || gotREST[0] != tc.hit.uri {
			t.Errorf("%s: rpc %v rest %v, want rest %s", tc.path, gotRPC, gotREST, tc.hit.uri)
		}
	}
}

func TestProxy_redirectNotFollowed(t *testing.T) {
	var secretHits int
	secret := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		secretHits++
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(secret.Close)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, secret.URL+"/admin", http.StatusFound)
	}))
	t.Cleanup(upstream.Close)

	p := mustProxy(t, upstream.URL, upstream.URL)
	rec := httptest.NewRecorder()
	p.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/chain/status", nil))
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status %d", rec.Code)
	}
	if rec.Header().Get("Location") != "" {
		t.Errorf("Location %q", rec.Header().Get("Location"))
	}
	if bytes.Contains(rec.Body.Bytes(), []byte(secret.URL)) {
		t.Errorf("body leaked redirect target %s", rec.Body.Bytes())
	}
	if secretHits != 0 {
		t.Fatalf("followed redirect, secret hits %d", secretHits)
	}
}

func TestProxy_unreachableHidesUpstream(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	url := upstream.URL
	upstream.Close()
	p := mustProxy(t, url, "http://127.0.0.1:31003")
	rec := httptest.NewRecorder()
	p.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/chain/status", nil))
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status %d", rec.Code)
	}
	if strings.TrimSpace(rec.Body.String()) != "chain unreachable" {
		t.Fatalf("body %q", rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), url) {
		t.Fatalf("body contains upstream url: %s", rec.Body.String())
	}
}

func TestProxy_oversizedBodyRefused(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("0123456789"))
	}))
	t.Cleanup(upstream.Close)
	p := mustProxy(t, upstream.URL, upstream.URL)
	p.maxBody = 4
	rec := httptest.NewRecorder()
	p.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/chain/status", nil))
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status %d", rec.Code)
	}
	if bytes.Contains(rec.Body.Bytes(), []byte("0123456789")) {
		t.Fatalf("partial body was written: %q", rec.Body.Bytes())
	}
}

func TestRegister_misconfiguredRefuses(t *testing.T) {
	t.Setenv("ORAMA_CHAIN_RPC_URL", "ftp://127.0.0.1:31001")
	t.Setenv("ORAMA_CHAIN_REST_URL", "")
	mux := http.NewServeMux()
	if err := Register(mux); err == nil {
		t.Fatal("Register accepted a bad RPC URL")
	}
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/chain/status", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status %d, body %s", rec.Code, rec.Body.String())
	}
}

func TestRegister_mountsAllowlist(t *testing.T) {
	body := []byte(`{"ok":true}`)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/status" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	}))
	t.Cleanup(upstream.Close)
	t.Setenv("ORAMA_CHAIN_RPC_URL", upstream.URL)
	t.Setenv("ORAMA_CHAIN_REST_URL", upstream.URL)

	mux := http.NewServeMux()
	if err := Register(mux); err != nil {
		t.Fatalf("Register: %v", err)
	}
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/chain/status", nil))
	if rec.Code != http.StatusOK || !bytes.Equal(rec.Body.Bytes(), body) {
		t.Fatalf("status %d body %q", rec.Code, rec.Body.Bytes())
	}
	miss := httptest.NewRecorder()
	mux.ServeHTTP(miss, httptest.NewRequest(http.MethodGet, "/v1/chain/broadcast_tx_commit", nil))
	if miss.Code != http.StatusNotFound {
		t.Fatalf("disallowed status %d", miss.Code)
	}
}

func mustProxy(t *testing.T, rpc, rest string) *Proxy {
	t.Helper()
	p, err := New(Config{RPCURL: rpc, RESTURL: rest, IndexURL: "http://127.0.0.1:9"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return p
}
