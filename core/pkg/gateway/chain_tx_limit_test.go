package gateway

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/logging"
)

func chainTxGateway(t *testing.T, sim, bcast *chainTxLimiter) (http.Handler, *int) {
	t.Helper()
	logger, _ := logging.NewColoredLogger(logging.ComponentGateway, false)
	g := &Gateway{
		logger:                logger,
		rateLimiter:           NewRateLimiter(100000, 100000),
		chainSimulateLimiter:  sim,
		chainBroadcastLimiter: bcast,
	}
	served := 0
	return g.rateLimitMiddleware(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) { served++ })), &served
}

// directClient is a request from the public address 198.51.100.n with no forwarding header.
func directClient(path string, n int) *http.Request {
	return request(fmt.Sprintf("198.51.100.%d:9999", n), "", path)
}

func fromClient(path string, n int) *http.Request {
	return request("127.0.0.1:9999", fmt.Sprintf("198.51.100.%d", n), path)
}

// Each transaction route has a bucket of its own: a client that exhausts broadcast is still served
// simulate, the explorer's routes and a GET of the same path, and the refusal is the canonical
// retryable envelope with a Retry-After.
func TestChainTxRoutes_haveBucketsOfTheirOwn(t *testing.T) {
	handler, served := chainTxGateway(t,
		newChainTxLimiter(60, 5, 6000, 1000), newChainTxLimiter(60, 2, 6000, 1000))

	var last *httptest.ResponseRecorder
	for i := 0; i < 6; i++ {
		last = httptest.NewRecorder()
		handler.ServeHTTP(last, fromClient(chainBroadcastPath, 7))
	}
	if *served != 2 || last.Code != http.StatusTooManyRequests {
		t.Fatalf("served %d of 6 broadcasts with last code %d; want the burst of 2 and a 429", *served, last.Code)
	}
	if last.Header().Get("Retry-After") == "" {
		t.Error("the 429 has no Retry-After")
	}

	*served = 0
	handler.ServeHTTP(httptest.NewRecorder(), fromClient(chainSimulatePath, 7))
	handler.ServeHTTP(httptest.NewRecorder(), fromClient("/v1/chain/status", 7))
	handler.ServeHTTP(httptest.NewRecorder(), fromClient(chainBroadcastPath, 8))
	if *served != 3 {
		t.Errorf("%d of 3 other requests were served; the broadcast bucket must not apply to them, nor to another client", *served)
	}

	*served = 0
	for i := 0; i < 4; i++ {
		get := httptest.NewRequest(http.MethodGet, chainBroadcastPath, nil)
		get.RemoteAddr = "127.0.0.1:9999"
		get.Header.Set("X-Forwarded-For", "198.51.100.7")
		handler.ServeHTTP(httptest.NewRecorder(), get)
	}
	if *served != 4 {
		t.Errorf("%d of 4 GETs were served; only a POST draws on the transaction buckets", *served)
	}
}

// Many addresses together cannot lift the load past the route's own bucket, and an address its own
// bucket refuses takes nothing from it.
func TestChainTxRoutes_theRouteBucketBoundsAllAddressesTogether(t *testing.T) {
	handler, served := chainTxGateway(t, newChainTxLimiter(6000, 1000, 60, 4), newChainTxLimiter(6000, 1000, 60, 4))
	for i := 0; i < 20; i++ {
		handler.ServeHTTP(httptest.NewRecorder(), fromClient(chainSimulatePath, i+1))
	}
	if *served != 4 {
		t.Fatalf("served %d of 20 simulates from 20 addresses, want the route's burst of 4", *served)
	}

	l := newChainTxLimiter(60, 1, 60, 3)
	l.allow("a")
	for i := 0; i < 10; i++ {
		l.allow("a")
	}
	if !l.allow("b") || !l.allow("c") {
		t.Error("an address refused by its own bucket used up the route's")
	}
}

func TestChainTxRoutes_areConfiguredAndTighterThanTheGeneralBucket(t *testing.T) {
	logger, _ := logging.NewColoredLogger(logging.ComponentGateway, false)
	g := &Gateway{logger: logger}
	configureRateLimiters(g)
	if g.chainSimulateLimiter == nil || g.chainBroadcastLimiter == nil {
		t.Fatal("a transaction route has no limiter")
	}
	for name, l := range map[string]*chainTxLimiter{"simulate": g.chainSimulateLimiter, "broadcast": g.chainBroadcastLimiter} {
		if l.perAddress.burst >= g.rateLimiter.burst/10 || l.perAddress.rate >= g.rateLimiter.rate/10 {
			t.Errorf("%s per-address bucket (%.2f/s, burst %d) is not far tighter than the general one", name, l.perAddress.rate, l.perAddress.burst)
		}
	}
	if g.chainBroadcastLimiter.perAddress.rate >= g.chainSimulateLimiter.perAddress.rate {
		t.Error("a broadcast, a write to every mempool, is not limited tighter than a simulate")
	}
}

// The faucet route has buckets of its own, the tightest of the chain routes: each drip is a
// transaction the faucet account signs and pays for, one at a time.
func TestChainTxRoutes_theFaucetHasTheTightestBucketsOfItsOwn(t *testing.T) {
	logger, _ := logging.NewColoredLogger(logging.ComponentGateway, false)
	g := &Gateway{logger: logger}
	configureRateLimiters(g)
	if g.chainFaucetLimiter == nil {
		t.Fatal("the faucet route has no limiter")
	}
	if got := g.chainTxLimiterFor(httptest.NewRequest(http.MethodPost, chainFaucetPath, nil)); got != g.chainFaucetLimiter {
		t.Fatalf("POST %s draws on %p, want the faucet limiter %p", chainFaucetPath, got, g.chainFaucetLimiter)
	}
	if got := g.chainTxLimiterFor(httptest.NewRequest(http.MethodGet, chainFaucetPath, nil)); got != nil {
		t.Error("a GET of the faucet route draws on a limiter")
	}
	faucet, broadcast := g.chainFaucetLimiter, g.chainBroadcastLimiter
	if faucet.perAddress.rate >= broadcast.perAddress.rate || faucet.perAddress.burst > broadcast.perAddress.burst {
		t.Errorf("a client's faucet bucket (%.3f/s, burst %d) is not tighter than broadcast's (%.3f/s, burst %d)",
			faucet.perAddress.rate, faucet.perAddress.burst, broadcast.perAddress.rate, broadcast.perAddress.burst)
	}
}

func TestChainTxRoutes_oneClientCannotUseUpTheFaucetForOthers(t *testing.T) {
	logger, _ := logging.NewColoredLogger(logging.ComponentGateway, false)
	g := &Gateway{
		logger:             logger,
		rateLimiter:        NewRateLimiter(100000, 100000),
		chainFaucetLimiter: newChainTxLimiter(chainFaucetPerAddressPerMinute, chainFaucetPerAddressBurst, chainFaucetRoutePerMinute, chainFaucetRouteBurst),
	}
	served := 0
	handler := g.rateLimitMiddleware(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) { served++ }))

	var last *httptest.ResponseRecorder
	for i := 0; i < 10; i++ {
		last = httptest.NewRecorder()
		handler.ServeHTTP(last, directClient(chainFaucetPath, 5))
	}
	if served != chainFaucetPerAddressBurst || last.Code != http.StatusTooManyRequests {
		t.Fatalf("one client was served %d of 10 asks with last code %d; want its burst of %d and a 429", served, last.Code, chainFaucetPerAddressBurst)
	}
	// The first client's three asks took three tokens of the route's bucket; its seven refused
	// asks took none, so the rest of the burst is left for other clients.
	served = 0
	for n := 6; n < 6+chainFaucetRouteBurst; n++ {
		handler.ServeHTTP(httptest.NewRecorder(), directClient(chainFaucetPath, n))
	}
	if want := chainFaucetRouteBurst - chainFaucetPerAddressBurst; served != want {
		t.Errorf("other clients were served %d, want %d: a refused client must take nothing from the route's bucket", served, want)
	}
	handler.ServeHTTP(last, directClient(chainFaucetPath, 200))
	if last.Code != http.StatusTooManyRequests {
		t.Errorf("a client past the route's burst got %d, want 429: many addresses must not lift the route's limit", last.Code)
	}
}

// A caller on this machine is the loopback address whatever it writes in X-Forwarded-For: a name
// it makes up is not a bucket of its own, and neither is the name an overlay peer forwards.
func TestChainTxRoutes_aForwardedForNamesNoFaucetBucketOfItsOwn(t *testing.T) {
	logger, _ := logging.NewColoredLogger(logging.ComponentGateway, false)
	g := &Gateway{
		logger:             logger,
		rateLimiter:        NewRateLimiter(100000, 100000),
		chainFaucetLimiter: newChainTxLimiter(chainFaucetPerAddressPerMinute, chainFaucetPerAddressBurst, chainFaucetRoutePerMinute, chainFaucetRouteBurst),
	}
	for name, remote := range map[string]string{"loopback": "127.0.0.1:9999", "an overlay peer": "10.0.0.2:9999"} {
		served := 0
		handler := g.rateLimitMiddleware(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) { served++ }))
		g.chainFaucetLimiter = newChainTxLimiter(chainFaucetPerAddressPerMinute, chainFaucetPerAddressBurst, chainFaucetRoutePerMinute, chainFaucetRouteBurst)
		for n := 1; n <= 20; n++ {
			handler.ServeHTTP(httptest.NewRecorder(), request(remote, fmt.Sprintf("198.51.100.%d", n), chainFaucetPath))
		}
		if served != chainFaucetPerAddressBurst {
			t.Errorf("%s: %d of 20 asks naming 20 networks were served, want the one bucket's burst of %d", name, served, chainFaucetPerAddressBurst)
		}
	}
}

// The faucet mints for whoever asks, so a caller on this machine without a forwarding header, which
// every other route lets through, is held to the faucet's buckets: tenant code runs here too.
func TestChainTxRoutes_theFaucetLimitsLocalCallersToo(t *testing.T) {
	logger, _ := logging.NewColoredLogger(logging.ComponentGateway, false)
	g := &Gateway{
		logger:             logger,
		rateLimiter:        NewRateLimiter(100000, 100000),
		chainFaucetLimiter: newChainTxLimiter(chainFaucetPerAddressPerMinute, chainFaucetPerAddressBurst, chainFaucetRoutePerMinute, chainFaucetRouteBurst),
	}
	served := 0
	handler := g.rateLimitMiddleware(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) { served++ }))
	local := func(method, path string) *http.Request {
		req := httptest.NewRequest(method, path, nil)
		req.RemoteAddr = "127.0.0.1:40000"
		return req
	}
	for i := 0; i < 10; i++ {
		handler.ServeHTTP(httptest.NewRecorder(), local(http.MethodPost, chainFaucetPath))
	}
	if served != chainFaucetPerAddressBurst {
		t.Errorf("a local caller was served %d of 10 faucet requests, want its burst of %d", served, chainFaucetPerAddressBurst)
	}
	served = 0
	for _, path := range []string{"/v1/chain/status", chainBroadcastPath} {
		handler.ServeHTTP(httptest.NewRecorder(), local(http.MethodPost, path))
	}
	handler.ServeHTTP(httptest.NewRecorder(), local(http.MethodGet, chainFaucetPath))
	if served != 3 {
		t.Errorf("%d of 3 other local requests were served: only a POST to the faucet is held to its buckets", served)
	}
}
