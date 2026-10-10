package chainread

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// The proxy keeps no copy of a read: a chain status that is hours old can only come from a layer
// outside it. These tests pin that every read reaches the node, and that no answer can be stored.
func TestProxy_everyReadReachesTheNodeAndNothingIsStorable(t *testing.T) {
	var hits atomic.Int64
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := hits.Add(1)
		w.Header().Set("Content-Type", "application/json")
		// A node that would let a client or a shared cache keep the answer.
		w.Header().Set("Cache-Control", "public, max-age=86400")
		w.Header().Set("ETag", `"v1"`)
		w.Header().Set("Last-Modified", "Fri, 09 Oct 2026 20:20:00 GMT")
		_, _ = fmt.Fprintf(w, `{"jsonrpc":"2.0","id":-1,"result":{"sync_info":{"latest_block_height":"%d"},"height":"%d"},"n":%d}`, 4300+n, 4300+n, n)
	}))
	t.Cleanup(upstream.Close)
	p, err := New(Config{RPCURL: upstream.URL, RESTURL: upstream.URL, IndexURL: upstream.URL})
	if err != nil {
		t.Fatal(err)
	}

	paths := []string{
		"/v1/chain/status",
		"/v1/chain/block?height=5",
		"/v1/chain/blocks?min_height=1&max_height=2",
		"/v1/chain/tx?hash=" + strings.Repeat("ab", 32),
		"/v1/chain/validators",
		"/v1/chain/supply/norama",
		"/v1/chain/staking/pool",
		"/v1/chain/index/status",
		"/v1/chain/index/epochs",
		"/v1/chain/index/epochs/3",
		"/v1/chain/index/supply",
		"/v1/chain/index/validators",
		"/v1/chain/index/stats/daily",
	}
	for _, path := range paths {
		var bodies [2]string
		for i := range bodies {
			before := hits.Load()
			rec := httptest.NewRecorder()
			p.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
			if rec.Code != http.StatusOK {
				t.Fatalf("%s: status %d: %s", path, rec.Code, rec.Body)
			}
			if got := hits.Load() - before; got != 1 {
				t.Errorf("%s request %d: the node was asked %d times, want 1", path, i+1, got)
			}
			if cc := rec.Header().Values("Cache-Control"); len(cc) != 1 || cc[0] != "no-store" {
				t.Errorf("%s: Cache-Control %v, want no-store", path, cc)
			}
			for _, h := range []string{"ETag", "Last-Modified", "Age", "Expires"} {
				if v := rec.Header().Get(h); v != "" {
					t.Errorf("%s: %s %q lets a cache keep the answer", path, h, v)
				}
			}
			bodies[i] = rec.Body.String()
		}
		if bodies[0] == bodies[1] {
			t.Errorf("%s: the second answer is the first again: %s", path, bodies[0])
		}
	}
}
