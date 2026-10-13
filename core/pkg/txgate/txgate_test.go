package txgate

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

const (
	account = "/cosmos/auth/v1beta1/accounts/orama1tehv5km5e9y706rc2gzk9yyun9dljjjny06dv2"
	txHash  = "/cosmos/tx/v1beta1/txs/0123456789ABCDEF0123456789abcdef0123456789ABCDEF0123456789abcdef"
)

// upstream records what reaches the chain API.
type upstream struct {
	mu    sync.Mutex
	calls []*http.Request
	body  []string
	reply func(w http.ResponseWriter, r *http.Request)
}

func (u *upstream) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	b, _ := io.ReadAll(r.Body)
	u.mu.Lock()
	u.calls = append(u.calls, r)
	u.body = append(u.body, string(b))
	u.mu.Unlock()
	if u.reply != nil {
		u.reply(w, r)
		return
	}
	w.Header().Set("X-Chain-Internal", "secret-detail")
	_, _ = io.WriteString(w, `{"ok":true}`)
}

func (u *upstream) count() int {
	u.mu.Lock()
	defer u.mu.Unlock()
	return len(u.calls)
}

func newGate(t *testing.T, mutate func(*Config), up *upstream) *Gate {
	t.Helper()
	srv := httptest.NewServer(up)
	t.Cleanup(srv.Close)
	c := Config{Upstream: srv.URL, Rate: 1000, Burst: 1000, InFlight: 8, UpstreamTimeout: 5 * time.Second}
	if mutate != nil {
		mutate(&c)
	}
	g, err := New(c)
	if err != nil {
		t.Fatal(err)
	}
	return g
}

func do(g *Gate, method, target, contentType, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	req.Header.Set("Cookie", "session=abc")
	req.Header.Set("X-Forwarded-For", "203.0.113.5")
	req.Header.Set("User-Agent", "wallet/1")
	rec := httptest.NewRecorder()
	g.ServeHTTP(rec, req)
	return rec
}

func TestGate_forwardsTheThreeWalletCalls(t *testing.T) {
	up := &upstream{}
	g := newGate(t, nil, up)
	for _, tc := range []struct{ method, path, ct, body string }{
		{"GET", account, "", ""},
		{"GET", txHash, "", ""},
		{"POST", "/cosmos/tx/v1beta1/txs", "application/json", `{"tx_bytes":"AAAA","mode":"BROADCAST_MODE_SYNC"}`},
		{"POST", "/cosmos/tx/v1beta1/txs", "application/json; charset=utf-8", `{"tx_bytes":"AAAA"}`},
	} {
		rec := do(g, tc.method, tc.path, tc.ct, tc.body)
		if rec.Code != 200 || strings.TrimSpace(rec.Body.String()) != `{"ok":true}` {
			t.Errorf("%s %s = %d %s", tc.method, tc.path, rec.Code, rec.Body)
		}
	}
	if up.count() != 4 {
		t.Fatalf("%d calls reached the chain API", up.count())
	}
	if up.body[2] != `{"tx_bytes":"AAAA","mode":"BROADCAST_MODE_SYNC"}` {
		t.Errorf("the transaction was altered: %s", up.body[2])
	}
}

func TestGate_nothingOfTheCallerCrossesAndNothingOfTheChainLeaks(t *testing.T) {
	up := &upstream{}
	g := newGate(t, nil, up)
	rec := do(g, "GET", account, "", "")
	r := up.calls[0]
	for _, h := range []string{"Cookie", "X-Forwarded-For", "User-Agent"} {
		if v := r.Header.Get(h); v != "" && !(h == "User-Agent" && strings.HasPrefix(v, "Go-http-client")) {
			t.Errorf("header %s = %q reached the chain API", h, v)
		}
	}
	if rec.Header().Get("X-Chain-Internal") != "" {
		t.Error("an upstream header was passed back to the caller")
	}
}

// Everything but the three calls is not served, whatever the chain API would answer.
func TestGate_refusesEverythingElse(t *testing.T) {
	up := &upstream{}
	g := newGate(t, nil, up)
	for _, path := range []string{
		"/", "/status", "/cosmos/bank/v1beta1/balances/orama1abc", "/cosmos/staking/v1beta1/validators",
		"/cosmos/tx/v1beta1/txs?query=message.sender%3D%27x%27", "/cosmos/tx/v1beta1/txs?x=1",
		"/cosmos/tx/v1beta1/txs/short", "/cosmos/tx/v1beta1/txs/" + strings.Repeat("g", 64),
		"/cosmos/tx/v1beta1/txs/" + strings.Repeat("a", 64) + "/extra",
		"/cosmos/tx/v1beta1/simulate", "/cosmos/auth/v1beta1/accounts", "/cosmos/auth/v1beta1/accounts/",
		"/cosmos/auth/v1beta1/accounts/orama1", "/cosmos/auth/v1beta1/accounts/cosmos1tehv5km5e9y706rc2gzk9yyun9dljjjny06dv2",
		"/cosmos/auth/v1beta1/accounts/ORAMA1TEHV5KM5E9Y706RC2GZK9YYUN9DLJJJNY06DV2",
		"/cosmos/auth/v1beta1/accounts/orama1tehv5km5e9y706rc2gzk9yyun9dljjjny06dv2/x",
		"/cosmos/auth/v1beta1/accounts/orama1tehv5km5e9y706rc2gzk9yyun9dljjjny06dv2?height=1",
		"/cosmos/auth/v1beta1/accounts/orama1%2e%2e", "/cosmos/tx/v1beta1/txs/%2e%2e/..",
		"//cosmos/tx/v1beta1/txs", "/cosmos/tx/v1beta1/txs/",
	} {
		rec := do(g, "GET", path, "", "")
		if rec.Code != http.StatusNotFound {
			t.Errorf("GET %s = %d", path, rec.Code)
		}
		rec = do(g, "POST", path, "application/json", `{}`)
		if rec.Code != http.StatusNotFound {
			t.Errorf("POST %s = %d", path, rec.Code)
		}
	}
	if n := up.count(); n != 0 {
		t.Fatalf("%d refused requests reached the chain API", n)
	}
}

func TestGate_wrongMethodOnAServedPath(t *testing.T) {
	up := &upstream{}
	g := newGate(t, nil, up)
	for _, tc := range []struct{ method, path, allow string }{
		{"POST", account, "GET"}, {"DELETE", txHash, "GET"}, {"GET", "/cosmos/tx/v1beta1/txs", "POST"}, {"PUT", "/cosmos/tx/v1beta1/txs", "POST"},
	} {
		rec := do(g, tc.method, tc.path, "application/json", "{}")
		if rec.Code != http.StatusMethodNotAllowed || rec.Header().Get("Allow") != tc.allow {
			t.Errorf("%s %s = %d allow %q", tc.method, tc.path, rec.Code, rec.Header().Get("Allow"))
		}
	}
	if up.count() != 0 {
		t.Fatal("a wrong method reached the chain API")
	}
}

func TestGate_broadcastBodyAndContentType(t *testing.T) {
	up := &upstream{}
	g := newGate(t, nil, up)
	big := `{"tx_bytes":"` + strings.Repeat("A", MaxBody) + `"}`
	if rec := do(g, "POST", "/cosmos/tx/v1beta1/txs", "application/json", big); rec.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("oversized body = %d", rec.Code)
	}
	if rec := do(g, "POST", "/cosmos/tx/v1beta1/txs", "text/plain", `{}`); rec.Code != http.StatusUnsupportedMediaType {
		t.Errorf("text body = %d", rec.Code)
	}
	if rec := do(g, "POST", "/cosmos/tx/v1beta1/txs", "", `{}`); rec.Code != http.StatusUnsupportedMediaType {
		t.Errorf("untyped body = %d", rec.Code)
	}
	justUnder := `{"tx_bytes":"` + strings.Repeat("A", MaxBody-64) + `"}`
	if rec := do(g, "POST", "/cosmos/tx/v1beta1/txs", "application/json", justUnder); rec.Code != 200 {
		t.Errorf("a body under the limit = %d", rec.Code)
	}
	if up.count() != 1 {
		t.Fatalf("%d calls reached the chain API", up.count())
	}
}

func TestGate_rateLimitIsForTheWholeGateAndRefills(t *testing.T) {
	now := time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)
	up := &upstream{}
	g := newGate(t, func(c *Config) {
		c.Rate, c.Burst = 2, 3
		c.Now = func() time.Time { return now }
	}, up)
	for i := 0; i < 3; i++ {
		if rec := do(g, "GET", account, "", ""); rec.Code != 200 {
			t.Fatalf("request %d = %d", i, rec.Code)
		}
	}
	rec := do(g, "GET", account, "", "")
	if rec.Code != http.StatusTooManyRequests || rec.Header().Get("Retry-After") == "" {
		t.Fatalf("over the burst = %d retry-after %q", rec.Code, rec.Header().Get("Retry-After"))
	}
	now = now.Add(time.Second) // two tokens
	for i := 0; i < 2; i++ {
		if rec := do(g, "GET", account, "", ""); rec.Code != 200 {
			t.Fatalf("after refill %d = %d", i, rec.Code)
		}
	}
	if rec := do(g, "GET", account, "", ""); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("past the refill = %d", rec.Code)
	}
	if up.count() != 5 {
		t.Fatalf("%d requests reached the chain API", up.count())
	}
}

func TestGate_inFlightCap(t *testing.T) {
	release := make(chan struct{})
	entered := make(chan struct{})
	up := &upstream{reply: func(w http.ResponseWriter, _ *http.Request) {
		entered <- struct{}{}
		<-release
		_, _ = io.WriteString(w, "{}")
	}}
	g := newGate(t, func(c *Config) { c.InFlight = 1 }, up)
	done := make(chan int)
	go func() { done <- do(g, "GET", account, "", "").Code }()
	<-entered
	if rec := do(g, "GET", txHash, "", ""); rec.Code != http.StatusTooManyRequests {
		t.Errorf("a second request while one is in flight = %d", rec.Code)
	}
	close(release)
	if code := <-done; code != 200 {
		t.Errorf("the first request = %d", code)
	}
}

func TestGate_upstreamFailuresDoNotLeakDetail(t *testing.T) {
	t.Run("chain API down", func(t *testing.T) {
		srv := httptest.NewServer(http.NotFoundHandler())
		addr := srv.URL
		srv.Close()
		g, err := New(Config{Upstream: addr, Rate: 10, Burst: 10, InFlight: 2, UpstreamTimeout: time.Second})
		if err != nil {
			t.Fatal(err)
		}
		rec := do(g, "GET", account, "", "")
		if rec.Code != http.StatusBadGateway || strings.Contains(rec.Body.String(), "127.0.0.1") || strings.Contains(rec.Body.String(), "connect") {
			t.Errorf("down upstream = %d %s", rec.Code, rec.Body)
		}
	})
	t.Run("chain answers an error", func(t *testing.T) {
		up := &upstream{reply: func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNotFound)
			_, _ = io.WriteString(w, `{"code":5,"message":"account not found"}`)
		}}
		g := newGate(t, nil, up)
		rec := do(g, "GET", account, "", "")
		if rec.Code != http.StatusNotFound || !strings.Contains(rec.Body.String(), "account not found") {
			t.Errorf("a chain refusal = %d %s", rec.Code, rec.Body)
		}
	})
	t.Run("answer too large", func(t *testing.T) {
		up := &upstream{reply: func(w http.ResponseWriter, _ *http.Request) {
			_, _ = io.WriteString(w, strings.Repeat("x", maxResponse+10))
		}}
		g := newGate(t, nil, up)
		if rec := do(g, "GET", account, "", ""); rec.Code != http.StatusBadGateway {
			t.Errorf("huge answer = %d", rec.Code)
		}
	})
	t.Run("redirect is not followed", func(t *testing.T) {
		up := &upstream{reply: func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, "http://203.0.113.9/steal", http.StatusFound)
		}}
		g := newGate(t, nil, up)
		if rec := do(g, "GET", account, "", ""); rec.Code != http.StatusFound {
			t.Errorf("redirect = %d", rec.Code)
		}
	})
}

func TestNew_validation(t *testing.T) {
	ok := Config{Upstream: "http://127.0.0.1:31003", Rate: 1, Burst: 1, InFlight: 1, UpstreamTimeout: time.Second}
	if _, err := New(ok); err != nil {
		t.Fatal(err)
	}
	cases := map[string]func(*Config){
		"https upstream":    func(c *Config) { c.Upstream = "https://127.0.0.1:31003" },
		"upstream path":     func(c *Config) { c.Upstream = "http://127.0.0.1:31003/cosmos" },
		"upstream query":    func(c *Config) { c.Upstream = "http://127.0.0.1:31003?x=1" },
		"upstream userinfo": func(c *Config) { c.Upstream = "http://u:p@127.0.0.1:31003" },
		"no upstream":       func(c *Config) { c.Upstream = "" },
		"no host":           func(c *Config) { c.Upstream = "http://" },
		"zero rate":         func(c *Config) { c.Rate = 0 },
		"zero burst":        func(c *Config) { c.Burst = 0 },
		"zero in flight":    func(c *Config) { c.InFlight = 0 },
		"zero timeout":      func(c *Config) { c.UpstreamTimeout = 0 },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			c := ok
			mutate(&c)
			if _, err := New(c); err == nil {
				t.Fatal("accepted")
			}
		})
	}
}
