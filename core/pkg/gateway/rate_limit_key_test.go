package gateway

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/logging"
)

func request(remoteAddr, forwardedFor, path string) *http.Request {
	r := httptest.NewRequest(http.MethodPost, path, nil)
	r.RemoteAddr = remoteAddr
	if forwardedFor != "" {
		r.Header.Set("X-Forwarded-For", forwardedFor)
	}
	return r
}

// The bug: rate limiting keyed on the first X-Forwarded-For entry, and any
// address in the WireGuard subnet was exempt from every limit. One header
// removed all rate limiting, including from the endpoints that mint
// credentials.
func TestRateLimitClient_aForgedForwardedForDoesNotExempt(t *testing.T) {
	// Caddy proxies from loopback and appends the real client, so the last
	// entry is real and everything before it is the caller's invention.
	client, exempt := rateLimitClient(request("127.0.0.1:9999", "10.0.0.1, 203.0.113.7", "/v1/auth/challenge"))

	if exempt {
		t.Fatal("a forged internal address exempted the caller from every limit")
	}
	if client != "203.0.113.7" {
		t.Errorf("client %q, want the address Caddy appended", client)
	}
}

func TestRateLimitClient_theWholeChainCanBeForged(t *testing.T) {
	// Every entry but the last is the caller's, however many they send.
	client, exempt := rateLimitClient(request("127.0.0.1:9999",
		"10.0.0.1, 10.0.0.2, 127.0.0.1, 198.51.100.9", "/v1/auth/verify"))

	if exempt {
		t.Fatal("a forged chain exempted the caller")
	}
	if client != "198.51.100.9" {
		t.Errorf("client %q, want the last entry", client)
	}
}

// A direct connection from off the node: the header is entirely the caller's
// invention and must be ignored.
func TestRateLimitClient_aDirectCallerCannotForward(t *testing.T) {
	client, exempt := rateLimitClient(request("203.0.113.7:5000", "10.0.0.1", "/v1/auth/challenge"))

	if exempt {
		t.Fatal("a direct caller exempted itself with a header")
	}
	if client != "203.0.113.7" {
		t.Errorf("client %q, want the peer address", client)
	}
}

// Another node's service reaching this gateway over the overlay is internal
// traffic, and the mesh is not reachable from outside.
func TestRateLimitClient_theOverlayIsExempt(t *testing.T) {
	client, exempt := rateLimitClient(request("10.0.0.4:40000", "", "/v1/db/query"))

	if !exempt {
		t.Error("traffic from another node over the overlay was rate limited")
	}
	if client != "10.0.0.4" {
		t.Errorf("client %q", client)
	}
}

// A process on this machine talking to the gateway directly, with nothing
// forwarded, is local. This is the branch that must not be widened: every
// public request also arrives from 127.0.0.1, so exempting loopback whenever
// it appears would exempt the internet.
func TestRateLimitClient_aLocalCallerIsExemptOnlyWithNothingForwarded(t *testing.T) {
	_, exempt := rateLimitClient(request("127.0.0.1:9999", "", "/v1/health"))
	if !exempt {
		t.Error("a process on this machine was rate limited")
	}

	_, exempt = rateLimitClient(request("127.0.0.1:9999", "203.0.113.7", "/v1/health"))
	if exempt {
		t.Fatal("a request that came through Caddy was treated as local — that exempts " +
			"every public request, because they all arrive from 127.0.0.1")
	}
}

func TestRateLimitClient_ignoresAnUnparseableForwardedFor(t *testing.T) {
	client, exempt := rateLimitClient(request("127.0.0.1:9999", "not-an-address", "/v1/health"))

	if !exempt || client != "127.0.0.1" {
		t.Errorf("client %q exempt %v; garbage in the header must not decide anything",
			client, exempt)
	}
}

func TestIsAuthRateLimitPath(t *testing.T) {
	for _, path := range []string{
		"/v1/auth/challenge", "/v1/auth/verify", "/v1/auth/api-key",
		"/v1/auth/token", "/v1/auth/refresh",
		// Approving a device link hands a new device a session.
		"/v1/auth/devices/approve",
	} {
		if !isAuthRateLimitPath(path) {
			t.Errorf("%s is not rate limited as a credential path", path)
		}
	}
	for _, path := range []string{"/v1/health", "/v1/db/query", "/v1/auth/whoami", "/v1/auth/devices",
		// Removed with the Phantom flow: a prefix here would give a tight
		// bucket to paths that no longer exist.
		"/v1/auth/phantom/session"} {
		if isAuthRateLimitPath(path) {
			t.Errorf("%s is rate limited as a credential path", path)
		}
	}
}

// The middleware, not just the key function: a forged header must not get past
// the auth-path bucket either.
func TestRateLimitMiddleware_aForgedHeaderIsStillLimitedOnTheAuthPath(t *testing.T) {
	logger, _ := logging.NewColoredLogger(logging.ComponentGateway, false)
	g := &Gateway{
		logger:          logger,
		rateLimiter:     NewRateLimiter(100000, 100000),
		authRateLimiter: NewRateLimiter(60, 3),
	}

	served := 0
	handler := g.rateLimitMiddleware(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) { served++ }))

	var lastCode int
	for i := 0; i < 10; i++ {
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, request("127.0.0.1:9999", "10.0.0.1, 203.0.113.7", "/v1/auth/challenge"))
		lastCode = w.Code
	}

	if served >= 10 {
		t.Errorf("%d of 10 credential requests were served; the burst is 3", served)
	}
	if lastCode != http.StatusTooManyRequests {
		t.Errorf("the last request answered %d, want 429", lastCode)
	}
}

// The tighter bucket must not apply to everything else, or a busy application
// would be throttled to 30 requests a minute.
func TestRateLimitMiddleware_ordinaryPathsUseTheGeneralLimit(t *testing.T) {
	logger, _ := logging.NewColoredLogger(logging.ComponentGateway, false)
	g := &Gateway{
		logger:          logger,
		rateLimiter:     NewRateLimiter(100000, 100000),
		authRateLimiter: NewRateLimiter(60, 3),
	}

	served := 0
	handler := g.rateLimitMiddleware(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) { served++ }))

	for i := 0; i < 10; i++ {
		handler.ServeHTTP(httptest.NewRecorder(),
			request("127.0.0.1:9999", "203.0.113.7", "/v1/db/query"))
	}

	if served != 10 {
		t.Errorf("%d of 10 ordinary requests were served; the credential bucket is "+
			"being applied to everything", served)
	}
}

// Overlay traffic bypasses both buckets: the reconciler and the cross-node
// fan-outs run far above any per-client limit.
func TestRateLimitMiddleware_overlayTrafficIsNotLimited(t *testing.T) {
	logger, _ := logging.NewColoredLogger(logging.ComponentGateway, false)
	g := &Gateway{
		logger:          logger,
		rateLimiter:     NewRateLimiter(60, 1),
		authRateLimiter: NewRateLimiter(60, 1),
	}

	served := 0
	handler := g.rateLimitMiddleware(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) { served++ }))

	for i := 0; i < 5; i++ {
		handler.ServeHTTP(httptest.NewRecorder(), request("10.0.0.4:40000", "", "/v1/auth/token"))
	}

	if served != 5 {
		t.Errorf("%d of 5 overlay requests were served", served)
	}
}

// The credential bucket only helps if it is meaningfully tighter than the
// general one. Widening it to the general limit would leave the code in place
// and the protection gone.
func TestAuthRateLimiterIsMuchTighterThanTheGeneralOne(t *testing.T) {
	logger, _ := logging.NewColoredLogger(logging.ComponentGateway, false)
	g := &Gateway{logger: logger}
	configureRateLimiters(g)

	if g.authRateLimiter == nil || g.rateLimiter == nil {
		t.Fatal("a rate limiter is missing")
	}
	if g.authRateLimiter.rate >= g.rateLimiter.rate/10 {
		t.Errorf("the credential bucket allows %.2f/s against a general %.2f/s; it has to "+
			"be far tighter or grinding the auth path costs nothing",
			g.authRateLimiter.rate, g.rateLimiter.rate)
	}
	if g.authRateLimiter.burst >= g.rateLimiter.burst/10 {
		t.Errorf("the credential burst is %d against a general %d",
			g.authRateLimiter.burst, g.rateLimiter.burst)
	}
}

func TestIsChainQueryPath(t *testing.T) {
	for path, want := range map[string]bool{
		"/v1/chain/query/orama.nodes.v1.Query/Node": true,
		"/v1/chain/query/":                          true,
		"/v1/chain/status":                          false,
		"/v1/chain/index/blocks":                    false,
		"/v1/chain/queryx":                          false,
		"/v1/db/query":                              false,
	} {
		if got := isChainQueryPath(path); got != want {
			t.Errorf("isChainQueryPath(%q) = %v, want %v", path, got, want)
		}
	}
}

// Every module query runs on the chain process, so /v1/chain/query/ draws on a bucket of its own:
// a client that exhausts it is limited on that route and not on the ordinary ones, and a forged
// forwarding header does not move it to another bucket.
func TestRateLimitMiddleware_chainQueriesHaveTheirOwnBucket(t *testing.T) {
	logger, _ := logging.NewColoredLogger(logging.ComponentGateway, false)
	g := &Gateway{
		logger:                logger,
		rateLimiter:           NewRateLimiter(100000, 100000),
		chainQueryRateLimiter: NewRateLimiter(60, 3),
	}
	served := 0
	handler := g.rateLimitMiddleware(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) { served++ }))

	var lastCode int
	for i := 0; i < 10; i++ {
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, request("127.0.0.1:9999", "10.0.0.1, 198.51.100.7", "/v1/chain/query/orama.nodes.v1.Query/Node"))
		lastCode = w.Code
	}
	if served != 3 || lastCode != http.StatusTooManyRequests {
		t.Errorf("served %d of 10 module queries with last code %d; want the burst of 3 and a 429", served, lastCode)
	}

	served = 0
	for i := 0; i < 10; i++ {
		handler.ServeHTTP(httptest.NewRecorder(), request("127.0.0.1:9999", "198.51.100.7", "/v1/chain/status"))
	}
	if served != 10 {
		t.Errorf("%d of 10 explorer requests were served; the query bucket must not apply to them", served)
	}
}

func TestChainQueryRateLimiterIsMuchTighterThanTheGeneralOne(t *testing.T) {
	logger, _ := logging.NewColoredLogger(logging.ComponentGateway, false)
	g := &Gateway{logger: logger}
	configureRateLimiters(g)
	if g.chainQueryRateLimiter == nil {
		t.Fatal("the chain query limiter is missing")
	}
	if g.chainQueryRateLimiter.rate >= g.rateLimiter.rate/10 || g.chainQueryRateLimiter.burst >= g.rateLimiter.burst/10 {
		t.Errorf("the chain query bucket (%.2f/s, burst %d) is not far tighter than the general one (%.2f/s, burst %d)",
			g.chainQueryRateLimiter.rate, g.chainQueryRateLimiter.burst, g.rateLimiter.rate, g.rateLimiter.burst)
	}
}

func TestBucketKey_ipv6ClientsShareTheirSlash64(t *testing.T) {
	cases := map[string]string{
		"203.0.113.7":                "203.0.113.7",
		"::ffff:203.0.113.7":         "203.0.113.7",
		"2001:db8:1:2:3:4:5:6":       "2001:db8:1:2::/64",
		"2001:db8:1:2:ffff:ffff:0:1": "2001:db8:1:2::/64",
		"2001:db8:1:3::1":            "2001:db8:1:3::/64",
		"fe80::1%eth0":               "fe80::/64",
		"not-an-address":             "not-an-address",
	}
	for in, want := range cases {
		if got := bucketKey(in); got != want {
			t.Errorf("bucketKey(%q) = %q, want %q", in, got, want)
		}
	}
}

// One IPv6 client can source requests from any address in its /64, so the query bucket is the /64:
// rotating the low 64 bits does not get a fresh burst, and another /64 has its own.
func TestRateLimitMiddleware_chainQueryBucketIsTheIPv6Slash64(t *testing.T) {
	logger, _ := logging.NewColoredLogger(logging.ComponentGateway, false)
	g := &Gateway{
		logger:                logger,
		rateLimiter:           NewRateLimiter(100000, 100000),
		chainQueryRateLimiter: NewRateLimiter(60, 3),
	}
	served := 0
	handler := g.rateLimitMiddleware(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) { served++ }))
	path := "/v1/chain/query/orama.nodes.v1.Query/Node"

	for i := 0; i < 20; i++ {
		client := fmt.Sprintf("2001:db8:1:2:%x::1", i+1)
		handler.ServeHTTP(httptest.NewRecorder(), request("127.0.0.1:9999", client, path))
	}
	if served != 3 {
		t.Errorf("served %d of 20 queries from one /64, want the burst of 3", served)
	}

	served = 0
	handler.ServeHTTP(httptest.NewRecorder(), request("127.0.0.1:9999", "2001:db8:1:3::1", path))
	if served != 1 {
		t.Error("a different /64 must have its own bucket")
	}
}
