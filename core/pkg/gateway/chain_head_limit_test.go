package gateway

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/logging"
)

// The explorer's reads of the chain indexer (/v1/chain/index/) draw on the general per-client bucket
// like every other route: they are not exempt, and a client that exhausts the bucket is limited on
// them with a 429.
func TestRateLimitMiddleware_theIndexRoutesDrawOnTheGeneralBucket(t *testing.T) {
	logger, _ := logging.NewColoredLogger(logging.ComponentGateway, false)
	g := &Gateway{logger: logger, rateLimiter: NewRateLimiter(60, 4)}
	served := 0
	handler := g.rateLimitMiddleware(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) { served++ }))

	var lastCode int
	for _, path := range []string{
		"/v1/chain/index/home", "/v1/chain/index/txs?filter=failed", "/v1/chain/index/blocks", "/v1/chain/index/epochs/3/rewards",
		"/v1/chain/index/accounts/orama1x/rewards", "/v1/chain/index/status",
	} {
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, behindCaddy(t, "10.0.0.1, 198.51.100.7", path))
		lastCode = w.Code
	}
	if served != 4 || lastCode != http.StatusTooManyRequests {
		t.Errorf("served %d of 6 index reads with last code %d; want the burst of 4 and a 429", served, lastCode)
	}

	served = 0
	handler.ServeHTTP(httptest.NewRecorder(), behindCaddy(t, "203.0.113.9", "/v1/chain/index/home"))
	if served != 1 {
		t.Error("another client network has its own bucket")
	}
}

func TestRateLimitMiddleware_headStreamsHaveTheirOwnBucket(t *testing.T) {
	logger, _ := logging.NewColoredLogger(logging.ComponentGateway, false)
	g := &Gateway{
		logger:               logger,
		rateLimiter:          NewRateLimiter(100000, 100000),
		chainHeadRateLimiter: NewRateLimiter(60, 3),
	}
	served := 0
	handler := g.rateLimitMiddleware(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) { served++ }))

	var lastCode int
	for i := 0; i < 10; i++ {
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, behindCaddy(t, "10.0.0.1, 198.51.100.7", "/v1/chain/head/stream"))
		lastCode = w.Code
	}
	if served != 3 || lastCode != http.StatusTooManyRequests {
		t.Errorf("served %d of 10 head streams with last code %d; want the burst of 3 and a 429", served, lastCode)
	}

	served = 0
	for i := 0; i < 10; i++ {
		handler.ServeHTTP(httptest.NewRecorder(), behindCaddy(t, "198.51.100.7", "/v1/chain/index/home"))
	}
	if served != 10 {
		t.Errorf("%d of 10 index reads were served; the stream bucket must not apply to them", served)
	}
}

func TestChainHeadRateLimiter_isMuchTighterThanTheGeneralOne(t *testing.T) {
	logger, _ := logging.NewColoredLogger(logging.ComponentGateway, false)
	g := &Gateway{logger: logger}
	configureRateLimiters(g)
	if g.chainHeadRateLimiter == nil {
		t.Fatal("the head stream limiter is missing")
	}
	if g.chainHeadRateLimiter.rate >= g.rateLimiter.rate/10 || g.chainHeadRateLimiter.burst >= g.rateLimiter.burst/10 {
		t.Errorf("the head stream bucket (%.2f/s, burst %d) is not far tighter than the general one (%.2f/s, burst %d)",
			g.chainHeadRateLimiter.rate, g.chainHeadRateLimiter.burst, g.rateLimiter.rate, g.rateLimiter.burst)
	}
	// A page held open all day reconnects about once every hundred seconds; its bucket must not run dry.
	if perMinute := float64(60) / 100; g.chainHeadRateLimiter.rate*60 < perMinute {
		t.Errorf("the head stream bucket refills at %.3f per minute, below the %.2f a page open all day uses", g.chainHeadRateLimiter.rate*60, perMinute)
	}
}

// A read of the indexer can cost a scan, so /v1/chain/index/ has a bucket of its own: a client that
// exhausts it is limited there and not on the other chain routes.
func TestRateLimitMiddleware_indexReadsHaveTheirOwnBucket(t *testing.T) {
	logger, _ := logging.NewColoredLogger(logging.ComponentGateway, false)
	g := &Gateway{
		logger:                logger,
		rateLimiter:           NewRateLimiter(100000, 100000),
		chainIndexRateLimiter: NewRateLimiter(60, 3),
	}
	served := 0
	handler := g.rateLimitMiddleware(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) { served++ }))

	var lastCode int
	for i := 0; i < 10; i++ {
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, behindCaddy(t, "10.0.0.1, 198.51.100.7", "/v1/chain/index/txs?page=1000&limit=100"))
		lastCode = w.Code
	}
	if served != 3 || lastCode != http.StatusTooManyRequests {
		t.Errorf("served %d of 10 index reads with last code %d; want the burst of 3 and a 429", served, lastCode)
	}

	served = 0
	for _, path := range []string{"/v1/chain/status", "/v1/chain/head/stream", "/v1/chain/indexer/x"} {
		handler.ServeHTTP(httptest.NewRecorder(), behindCaddy(t, "198.51.100.7", path))
	}
	if served != 3 {
		t.Errorf("%d of 3 other chain reads were served; the index bucket must apply to /v1/chain/index/ alone", served)
	}
}

// A wallet that scans the shielded pool reads many pages: those routes draw on the same per-client
// index bucket as every other index read, so a scanner cannot exceed it.
func TestRateLimitMiddleware_shieldedScanningRoutesDrawOnTheIndexBucket(t *testing.T) {
	logger, _ := logging.NewColoredLogger(logging.ComponentGateway, false)
	g := &Gateway{
		logger:                logger,
		rateLimiter:           NewRateLimiter(100000, 100000),
		chainIndexRateLimiter: NewRateLimiter(60, 3),
	}
	served := 0
	handler := g.rateLimitMiddleware(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) { served++ }))

	var lastCode int
	for _, path := range []string{
		"/v1/chain/index/shielded/actions?from_height=1&limit=1000", "/v1/chain/index/shielded/subtree-roots",
		"/v1/chain/index/shielded/actions?from_height=900", "/v1/chain/index/shielded/actions?from_height=1800",
		"/v1/chain/index/shielded/subtree-roots?start_index=3",
	} {
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, behindCaddy(t, "10.0.0.1, 198.51.100.7", path))
		lastCode = w.Code
	}
	if served != 3 || lastCode != http.StatusTooManyRequests {
		t.Errorf("served %d of 5 scanning reads with last code %d; want the burst of 3 and a 429", served, lastCode)
	}
}

func TestChainIndexRateLimiter_isFarAboveABrowsingPersonAndFarBelowTheGeneralBucket(t *testing.T) {
	logger, _ := logging.NewColoredLogger(logging.ComponentGateway, false)
	g := &Gateway{logger: logger}
	configureRateLimiters(g)
	if g.chainIndexRateLimiter == nil || g.chainHeadHolds == nil || g.chainIndexHolds == nil {
		t.Fatal("the index limiter or the holds are missing")
	}
	if g.chainIndexRateLimiter.rate*60 < 600 || g.chainIndexRateLimiter.burst < 100 {
		t.Errorf("the index bucket (%.2f/s, burst %d) is too small for a page that reads the index a dozen times and a home a block",
			g.chainIndexRateLimiter.rate, g.chainIndexRateLimiter.burst)
	}
	if g.chainIndexRateLimiter.rate >= g.rateLimiter.rate/5 || g.chainIndexRateLimiter.burst >= g.rateLimiter.burst/5 {
		t.Errorf("the index bucket (%.2f/s, burst %d) is not far tighter than the general one (%.2f/s, burst %d)",
			g.chainIndexRateLimiter.rate, g.chainIndexRateLimiter.burst, g.rateLimiter.rate, g.rateLimiter.burst)
	}
}

func TestHoldLimiter_boundsWhatOneClientHoldsAndForgetsWhoHoldsNothing(t *testing.T) {
	l := newHoldLimiter(2)
	if !l.acquire("a") || !l.acquire("a") {
		t.Fatal("two holds were refused")
	}
	if l.acquire("a") {
		t.Error("a third hold was granted")
	}
	if !l.acquire("b") {
		t.Error("another client was refused for a's holds")
	}
	l.release("a")
	if !l.acquire("a") {
		t.Error("a hold that was given back was not available")
	}
	l.release("a")
	l.release("a")
	l.release("b")
	l.release("nobody")
	if len(l.held) != 0 {
		t.Errorf("clients holding nothing are still kept: %v", l.held)
	}
}

// One client network may hold only so many head streams open, however fast its bucket would let it
// open them; a refused one takes nothing, and a stream that ends gives its place back.
func TestRateLimitMiddleware_aClientHoldsAtMostItsShareOfHeadStreams(t *testing.T) {
	logger, _ := logging.NewColoredLogger(logging.ComponentGateway, false)
	g := &Gateway{
		logger:         logger,
		rateLimiter:    NewRateLimiter(100000, 100000),
		chainHeadHolds: newHoldLimiter(2),
	}
	release := make(chan struct{})
	var inside sync.WaitGroup
	handler := g.rateLimitMiddleware(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		inside.Done()
		<-release
	}))

	var done sync.WaitGroup
	open := func(client string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		done.Add(1)
		go func() {
			defer done.Done()
			handler.ServeHTTP(w, behindCaddy(t, client, "/v1/chain/head/stream"))
		}()
		return w
	}
	inside.Add(3)
	open("198.51.100.7")
	open("198.51.100.7")
	open("203.0.113.9") // another client network has its own place
	inside.Wait()

	w := httptest.NewRecorder()
	handler.ServeHTTP(w, behindCaddy(t, "198.51.100.7", "/v1/chain/head/stream"))
	if w.Code != http.StatusTooManyRequests || w.Header().Get("Retry-After") == "" {
		t.Errorf("a third stream from one client: HTTP %d, Retry-After %q; want 429 with a retry", w.Code, w.Header().Get("Retry-After"))
	}

	close(release)
	done.Wait()
	g.chainHeadHolds.mu.Lock()
	held := len(g.chainHeadHolds.held)
	g.chainHeadHolds.mu.Unlock()
	if held != 0 {
		t.Errorf("%d clients still hold streams after every stream ended", held)
	}

	inside.Add(1)
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, behindCaddy(t, "198.51.100.7", "/v1/chain/head/stream"))
	if w.Code != http.StatusOK {
		t.Errorf("a stream after the others ended: HTTP %d, want 200", w.Code)
	}
}

// One client network may have only its share of the gateway's index reads in flight, so it cannot
// take every slot and have the others refused.
func TestRateLimitMiddleware_aClientHoldsAtMostItsShareOfIndexReads(t *testing.T) {
	logger, _ := logging.NewColoredLogger(logging.ComponentGateway, false)
	g := &Gateway{
		logger:          logger,
		rateLimiter:     NewRateLimiter(100000, 100000),
		chainIndexHolds: newHoldLimiter(2),
	}
	release := make(chan struct{})
	var inside sync.WaitGroup
	handler := g.rateLimitMiddleware(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		inside.Done()
		<-release
	}))
	var done sync.WaitGroup
	open := func(client string) {
		done.Add(1)
		go func() {
			defer done.Done()
			handler.ServeHTTP(httptest.NewRecorder(), behindCaddy(t, client, "/v1/chain/index/txs"))
		}()
	}
	inside.Add(3)
	open("198.51.100.7")
	open("198.51.100.7")
	open("203.0.113.9")
	inside.Wait()

	w := httptest.NewRecorder()
	handler.ServeHTTP(w, behindCaddy(t, "198.51.100.7", "/v1/chain/index/blocks"))
	if w.Code != http.StatusTooManyRequests || w.Header().Get("Retry-After") == "" {
		t.Errorf("a third read in flight from one client: HTTP %d, Retry-After %q; want 429 with a retry", w.Code, w.Header().Get("Retry-After"))
	}
	close(release)
	done.Wait()
	if len(g.chainIndexHolds.held) != 0 {
		t.Errorf("%d clients still hold reads after they ended", len(g.chainIndexHolds.held))
	}
	inside.Add(1)
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, behindCaddy(t, "198.51.100.7", "/v1/chain/index/blocks"))
	if w.Code != http.StatusOK {
		t.Errorf("a read after the others ended: HTTP %d, want 200", w.Code)
	}
}
