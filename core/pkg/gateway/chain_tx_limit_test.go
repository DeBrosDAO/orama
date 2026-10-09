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
