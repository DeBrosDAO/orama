package gateway

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/pkg/gateway/clientkey"
	"github.com/DeBrosOfficial/network/pkg/logging"
)

const testCallerProof = "feedfacefeedfacefeedfacefeedfacefeedfacefeedfacefeedfacefeedface"

// testCaddyProver is a prover holding testCallerProof, as a node's does after the installer ran.
func testCaddyProver(t *testing.T) *clientkey.Prover {
	t.Helper()
	path := filepath.Join(t.TempDir(), "orama-caddy-proof.key")
	if err := os.WriteFile(path, []byte(testCallerProof+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return clientkey.NewProver(path, nil)
}

// viaCaddy is a request as the node's Caddy forwards it: from loopback, with the address it is
// talking to appended to X-Forwarded-For and its proof in the header.
func viaCaddy(path string, clientAddr string, forgedEntries ...string) *http.Request {
	xff := clientAddr
	for _, forged := range forgedEntries {
		xff = forged + ", " + xff
	}
	r := request("127.0.0.1:9999", xff, path)
	r.Header.Set(clientkey.ProofHeader, testCallerProof)
	return r
}

func faucetGateway(t *testing.T) (http.Handler, *int) {
	t.Helper()
	logger, _ := logging.NewColoredLogger(logging.ComponentGateway, false)
	g := &Gateway{
		logger:             logger,
		rateLimiter:        NewRateLimiter(100000, 100000),
		chainFaucetLimiter: newChainTxLimiter(chainFaucetPerAddressPerMinute, chainFaucetPerAddressBurst, chainFaucetRoutePerMinute, chainFaucetRouteBurst),
		callerProof:        testCaddyProver(t),
	}
	served := 0
	inner := g.rateLimitMiddleware(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) { served++ }))
	return g.callerProof.Middleware(inner), &served
}

// Behind Caddy every public caller reaches the gateway from 127.0.0.1. With Caddy's proof each is
// its own client: one abuser exhausts its own faucet bucket and nobody else's.
func TestFaucet_callersBehindCaddyHaveBucketsOfTheirOwn(t *testing.T) {
	handler, served := faucetGateway(t)

	abuser := 0
	for i := 0; i < 10; i++ {
		before := *served
		handler.ServeHTTP(httptest.NewRecorder(), viaCaddy(chainFaucetPath, "203.0.113.7"))
		abuser += *served - before
	}
	if abuser != chainFaucetPerAddressBurst {
		t.Fatalf("one caller was served %d of 10, want its burst of %d", abuser, chainFaucetPerAddressBurst)
	}

	// Others are served: each has its own bucket, up to the route's own limit.
	for n := 1; n <= 2; n++ {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, viaCaddy(chainFaucetPath, fmt.Sprintf("203.0.113.%d", 100+n)))
		if rec.Code == http.StatusTooManyRequests {
			t.Errorf("caller %d was refused because another caller used up a bucket", n)
		}
	}
}

// What a client writes in X-Forwarded-For before Caddy's entry is its own invention: it is neither
// the key nor a way to a bucket of one's own.
func TestFaucet_aForgedEarlierEntryIsNotAKey(t *testing.T) {
	handler, served := faucetGateway(t)
	for n := 1; n <= 20; n++ {
		handler.ServeHTTP(httptest.NewRecorder(), viaCaddy(chainFaucetPath, "203.0.113.7", fmt.Sprintf("198.51.100.%d", n)))
	}
	if *served != chainFaucetPerAddressBurst {
		t.Errorf("%d of 20 asks naming 20 forged networks were served, want the one bucket's burst of %d", *served, chainFaucetPerAddressBurst)
	}
}

// A process on the node, or a client that reaches the gateway some other way, can write the
// header and the entry but not the secret: it is one client however many names it claims.
func TestFaucet_withoutTheProofXForwardedForNamesNoBucket(t *testing.T) {
	cases := map[string]func(n int) *http.Request{
		"no proof header": func(n int) *http.Request {
			return request("127.0.0.1:9999", fmt.Sprintf("198.51.100.%d", n), chainFaucetPath)
		},
		"a guessed proof": func(n int) *http.Request {
			r := request("127.0.0.1:9999", fmt.Sprintf("198.51.100.%d", n), chainFaucetPath)
			r.Header.Set(clientkey.ProofHeader, "0000000000000000000000000000000000000000000000000000000000000000")
			return r
		},
		"the proof from a peer off the node": func(n int) *http.Request {
			r := request("198.51.100.200:9999", fmt.Sprintf("198.51.100.%d", n), chainFaucetPath)
			r.Header.Set(clientkey.ProofHeader, testCallerProof)
			return r
		},
	}
	for name, makeRequest := range cases {
		handler, served := faucetGateway(t)
		for n := 1; n <= 20; n++ {
			handler.ServeHTTP(httptest.NewRecorder(), makeRequest(n))
		}
		if *served != chainFaucetPerAddressBurst {
			t.Errorf("%s: %d of 20 asks naming 20 networks were served, want the one bucket's burst of %d", name, *served, chainFaucetPerAddressBurst)
		}
	}
}

// The proof is checked and deleted by the chain the server actually runs, before anything can read
// or forward it, and it is what decides whom the rest of the gateway attributes the request to.
func TestWithMiddleware_deletesTheCallerProofAndAttributesByIt(t *testing.T) {
	logger, _ := logging.NewColoredLogger(logging.ComponentGateway, false)
	g := &Gateway{
		logger:      logger,
		cfg:         &Config{ClientNamespace: "index"},
		ready:       newReadiness(),
		startedAt:   time.Now(),
		callerProof: testCaddyProver(t),
	}
	g.ready.set(ReadinessReady, "", "")

	cases := map[string]struct {
		header string
		want   string
	}{
		"the secret":  {testCallerProof, "203.0.113.9"},
		"a wrong one": {"forged", "127.0.0.1"},
		"none at all": {"", "127.0.0.1"},
	}
	for name, tc := range cases {
		var seen http.Header
		var attributed string
		next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			seen = r.Header.Clone()
			attributed = getClientIP(r)
		})
		r := request("127.0.0.1:54321", "6.6.6.6, 203.0.113.9", "/v1/health")
		if tc.header != "" {
			r.Header.Set(clientkey.ProofHeader, tc.header)
		}
		g.withMiddleware(next).ServeHTTP(httptest.NewRecorder(), r)
		if seen == nil {
			t.Fatalf("%s: the request never reached the handler", name)
		}
		if v := seen.Get(clientkey.ProofHeader); v != "" {
			t.Errorf("%s: the proof header reached the handler as %q", name, v)
		}
		if attributed != tc.want {
			t.Errorf("%s: attributed to %q, want %q", name, attributed, tc.want)
		}
	}
}

// A gateway that has no secret (its file is not there) still never lets the header through.
func TestWithMiddleware_withoutAProverStillDeletesTheCallerProof(t *testing.T) {
	logger, _ := logging.NewColoredLogger(logging.ComponentGateway, false)
	g := &Gateway{
		logger:    logger,
		cfg:       &Config{ClientNamespace: "index"},
		ready:     newReadiness(),
		startedAt: time.Now(),
	}
	g.ready.set(ReadinessReady, "", "")
	var seen string
	r := viaCaddy("/v1/health", "203.0.113.9")
	g.withMiddleware(http.HandlerFunc(func(_ http.ResponseWriter, got *http.Request) {
		seen = got.Header.Get(clientkey.ProofHeader)
	})).ServeHTTP(httptest.NewRecorder(), r)
	if seen != "" {
		t.Errorf("the proof header reached the handler as %q", seen)
	}
}
