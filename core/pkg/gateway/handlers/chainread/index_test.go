package chainread

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// The bech32 orama account address of 20 zero bytes.
const testAccount = "orama1qqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqmg3rhc"

// indexUpstream records every request URI the indexer receives.
type indexUpstream struct {
	mu   sync.Mutex
	uris []string
}

func (u *indexUpstream) serve(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u.mu.Lock()
		u.uris = append(u.uris, r.RequestURI)
		u.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if strings.Contains(r.URL.Path, "/blocks/404") {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":"not indexed"}`))
			return
		}
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func (u *indexUpstream) seen() []string {
	u.mu.Lock()
	defer u.mu.Unlock()
	return append([]string(nil), u.uris...)
}

func indexProxy(t *testing.T) (*Proxy, *indexUpstream) {
	t.Helper()
	up := &indexUpstream{}
	srv := up.serve(t)
	p, err := New(Config{RPCURL: "http://127.0.0.1:9", RESTURL: "http://127.0.0.1:9", IndexURL: srv.URL})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return p, up
}

func TestIndexProxy_buildsEachUpstreamURL(t *testing.T) {
	hash := strings.Repeat("Ab", 32)
	cases := []struct{ path, want string }{
		{"/v1/chain/index/status", "/index/v1/status"},
		{"/v1/chain/index/blocks/42", "/index/v1/blocks/42"},
		{"/v1/chain/index/txs/0x" + hash, "/index/v1/txs/" + strings.ToLower(hash)},
		{"/v1/chain/index/accounts/" + testAccount + "/txs", "/index/v1/accounts/" + testAccount + "/txs"},
		{"/v1/chain/index/accounts/" + testAccount + "/txs?limit=100&page=1000", "/index/v1/accounts/" + testAccount + "/txs?limit=100&page=1000"},
		{"/v1/chain/index/cnft/assets/" + hash, "/index/v1/cnft/assets/" + strings.ToLower(hash)},
		{"/v1/chain/index/cnft/owners/" + testAccount + "/assets?page=2", "/index/v1/cnft/owners/" + testAccount + "/assets?page=2"},
	}
	for _, tc := range cases {
		p, up := indexProxy(t)
		rec := httptest.NewRecorder()
		p.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, tc.path, nil))
		if rec.Code != http.StatusOK {
			t.Errorf("%s: status %d body %q", tc.path, rec.Code, rec.Body.String())
			continue
		}
		if got := up.seen(); len(got) != 1 || got[0] != tc.want {
			t.Errorf("%s: upstream saw %v, want %s", tc.path, got, tc.want)
		}
	}
}

func TestIndexProxy_passesTheIndexersNotFound(t *testing.T) {
	p, _ := indexProxy(t)
	rec := httptest.NewRecorder()
	p.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/chain/index/blocks/404", nil))
	if rec.Code != http.StatusNotFound || !strings.Contains(rec.Body.String(), "not indexed") {
		t.Fatalf("status %d body %q", rec.Code, rec.Body.String())
	}
}

func TestIndexProxy_refusesBadParamsWithoutCallingTheIndexer(t *testing.T) {
	hash := strings.Repeat("ab", 32)
	cases := []struct {
		path string
		code int
	}{
		{"/v1/chain/index/status?x=1", http.StatusBadRequest},
		{"/v1/chain/index/blocks/2?full=1", http.StatusBadRequest},
		{"/v1/chain/index/accounts/" + testAccount + "/txs?limit=101", http.StatusBadRequest},
		{"/v1/chain/index/accounts/" + testAccount + "/txs?limit=0", http.StatusBadRequest},
		{"/v1/chain/index/accounts/" + testAccount + "/txs?page=1001", http.StatusBadRequest},
		{"/v1/chain/index/accounts/" + testAccount + "/txs?page=01", http.StatusBadRequest},
		{"/v1/chain/index/accounts/" + testAccount + "/txs?page=1&page=2", http.StatusBadRequest},
		{"/v1/chain/index/accounts/" + testAccount + "/txs?offset=3", http.StatusBadRequest},
		{"/v1/chain/index/accounts/" + testAccount + "/txs?limit=1;page=2", http.StatusBadRequest},
		{"/v1/chain/index/cnft/owners/" + testAccount + "/assets?limit=", http.StatusBadRequest},
		{"/v1/chain/index/cnft/assets/" + hash + "?x=1", http.StatusBadRequest},
		{"/v1/chain/index/blocks/0", http.StatusNotFound},
		{"/v1/chain/index/blocks/-1", http.StatusNotFound},
		{"/v1/chain/index/blocks/1e3", http.StatusNotFound},
		{"/v1/chain/index/txs/" + hash[:62], http.StatusNotFound},
		{"/v1/chain/index/accounts/" + strings.ToUpper(testAccount) + "/txs", http.StatusNotFound},
		{"/v1/chain/index/accounts/cosmos1qqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqnrql8a/txs", http.StatusNotFound},
		{"/v1/chain/index/accounts/orama1bbbb/txs", http.StatusNotFound},
		{"/v1/chain/index/accounts/orama1" + strings.Repeat("q", 415) + "/txs", http.StatusNotFound},
		{"/v1/chain/index/cnft/assets/" + hash + "00", http.StatusNotFound},
		{"/v1/chain/index/cnft/owners/" + testAccount, http.StatusNotFound},
		{"/v1/chain/index/", http.StatusNotFound},
		{"/v1/chain/index/blocks/1/txs", http.StatusNotFound},
		{"/v1/chain/index/blocks/1/", http.StatusNotFound},
		{"/v1/chain/index//blocks/1", http.StatusNotFound},
		{"/v1/chain/index/../status", http.StatusNotFound},
		{"/v1/chain/index/admin", http.StatusNotFound},
		{"/v1/chain/indexer/status", http.StatusNotFound},
	}
	for _, tc := range cases {
		p, up := indexProxy(t)
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/v1/chain/status", nil)
		req.URL.Path = strings.SplitN(tc.path, "?", 2)[0]
		if q := strings.SplitN(tc.path, "?", 2); len(q) == 2 {
			req.URL.RawQuery = q[1]
		}
		p.ServeHTTP(rec, req)
		if rec.Code != tc.code {
			t.Errorf("%s: status %d, want %d", tc.path, rec.Code, tc.code)
		}
		if got := up.seen(); len(got) != 0 {
			t.Errorf("%s: indexer was called with %v", tc.path, got)
		}
	}
}

func TestIndexProxy_refusesOtherMethodsAndEncodedPaths(t *testing.T) {
	p, up := indexProxy(t)
	rec := httptest.NewRecorder()
	p.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/chain/index/status", nil))
	if rec.Code != http.StatusMethodNotAllowed || rec.Header().Get("Allow") != http.MethodGet {
		t.Fatalf("POST: status %d allow %q", rec.Code, rec.Header().Get("Allow"))
	}
	encoded := httptest.NewRequest(http.MethodGet, "/v1/chain/index/%73tatus", nil)
	rec = httptest.NewRecorder()
	p.ServeHTTP(rec, encoded)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("encoded path: status %d", rec.Code)
	}
	if got := up.seen(); len(got) != 0 {
		t.Fatalf("indexer was called with %v", got)
	}
}
