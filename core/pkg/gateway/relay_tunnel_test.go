package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"

	"github.com/DeBrosOfficial/network/pkg/gateway/routepolicy"
	"github.com/DeBrosOfficial/network/pkg/httputil"
	"github.com/DeBrosOfficial/network/pkg/logging"
)

// Bugboard #266 — the anonymous relay of a relayed fetch. It takes no
// credential, so the destination pin, the limits and the absence of any
// fallback to a direct connection are the whole of its safety.

const relayBase = "dbrs.space"

func TestRelayHostAllowed_matrix(t *testing.T) {
	s := newRelayService([]string{relayBase})
	for host, want := range map[string]bool{
		"ns-anchat." + relayBase:               true,
		"NS-Anchat." + relayBase:               true,
		"ns-anchat." + relayBase + ".":         true,
		relayBase:                              true,
		"a.b.c." + relayBase:                   true,
		"evil" + relayBase:                     false, // not a label boundary
		"evil-" + relayBase:                    false,
		"ns-x." + relayBase + ".attacker.tld":  false,
		"evil-ns-x.base.attacker.tld":          false,
		"attacker.tld":                         false,
		"":                                     false,
		"1.2.3.4":                              false,
		"127.0.0.1":                            false,
		"10.0.0.1":                             false,
		"::1":                                  false,
		"[::1]":                                false,
		"2606:4700::1111":                      false,
		"ns-x." + relayBase + "@attacker.tld":  false,
		"attacker.tld/" + relayBase:            false,
		"ns-x." + relayBase + ":443":           false,
		"ns-x.localhost":                       false,
		"localhost":                            false,
		"ns-x." + relayBase + "\r\nHost: evil": false,
	} {
		_, ok := s.target(host, "443")
		if ok != want {
			t.Errorf("host %q: allowed = %v, want %v", host, ok, want)
		}
	}
}

func TestRelayTarget_portIs443Only(t *testing.T) {
	s := newRelayService([]string{relayBase})
	for port, want := range map[string]bool{
		"": true, "443": true, "80": false, "22": false, "8443": false, "0": false, "abc": false, "-1": false, "65979": false,
	} {
		_, ok := s.target("ns-a."+relayBase, port)
		if ok != want {
			t.Errorf("port %q: allowed = %v, want %v", port, ok, want)
		}
	}
}

func TestRelayTarget_aConfiguredListReplacesTheBaseDomain(t *testing.T) {
	cfg := &Config{BaseDomain: relayBase, RelayAllowedSuffixes: []string{"partner.example", "other.example"}}
	s := newRelayService(relayAllowedSuffixes(cfg))
	for host, want := range map[string]bool{
		"ns-a.partner.example": true, "other.example": true, "ns-a." + relayBase: false,
	} {
		if _, ok := s.target(host, "443"); ok != want {
			t.Errorf("host %q: allowed = %v, want %v", host, ok, want)
		}
	}
	if got := relayAllowedSuffixes(&Config{BaseDomain: relayBase}); len(got) != 1 || got[0] != relayBase {
		t.Errorf("the default allowlist = %v, want the base domain", got)
	}
	if got := relayAllowedSuffixes(&Config{}); len(got) != 0 {
		t.Errorf("a gateway with no base domain allows %v", got)
	}
}

func TestRelayService_poolIsBoundedAndReleasable(t *testing.T) {
	s := newRelayService(nil)
	var releases []func()
	for range relayMaxStreams {
		release, ok := s.acquire()
		if !ok {
			t.Fatal("refused below the bound")
		}
		releases = append(releases, release)
	}
	if _, ok := s.acquire(); ok {
		t.Error("accepted past the bound")
	}
	releases[0]()
	if _, ok := s.acquire(); !ok {
		t.Error("a released slot was not reusable")
	}
}

func relayTestGateway(t *testing.T, running bool, dial func(context.Context, string, string) (net.Conn, error)) *Gateway {
	t.Helper()
	g := newTunnelTestGateway(t)
	g.relay = newRelayService([]string{relayBase})
	g.relay.running = func() bool { return running }
	g.relay.dial = dial
	return g
}

func relayWSRequest(query string) *http.Request {
	r := httptest.NewRequest(http.MethodGet, relayPath+"?"+query, nil)
	r.Header.Set("Connection", "Upgrade")
	r.Header.Set("Upgrade", "websocket")
	return r
}

func wantRelayRefusal(t *testing.T, rec *httptest.ResponseRecorder, status int, code string) {
	t.Helper()
	if rec.Code != status {
		t.Fatalf("status = %d, want %d (body %s)", rec.Code, status, rec.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("body is not JSON: %v", err)
	}
	if body["code"] != code || body["error"] == "" || body["hint"] == "" {
		t.Errorf("body = %v, want {error, code %s, hint}", body, code)
	}
}

func TestRelayHandler_refusalsBeforeTheUpgradeCarryACode(t *testing.T) {
	var dialled atomic.Int32
	g := relayTestGateway(t, true, func(context.Context, string, string) (net.Conn, error) {
		dialled.Add(1)
		return nil, errors.New("no")
	})
	for name, query := range map[string]string{
		"a foreign host":   "host=example.com&port=443",
		"an IP literal":    "host=1.1.1.1&port=443",
		"the wrong port":   "host=ns-a." + relayBase + "&port=80",
		"no host":          "port=443",
		"lookalike suffix": "host=evil" + relayBase + "&port=443",
	} {
		rec := httptest.NewRecorder()
		g.relayTunnelHandler(rec, relayWSRequest(query))
		t.Run(name, func(t *testing.T) { wantRelayRefusal(t, rec, http.StatusBadRequest, CodeRelayDestinationNotAllowed) })
	}
	if dialled.Load() != 0 {
		t.Errorf("a refused destination was dialled %d times", dialled.Load())
	}

	rec := httptest.NewRecorder()
	g.relayTunnelHandler(rec, httptest.NewRequest(http.MethodPost, relayPath, nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST: status %d", rec.Code)
	}
	rec = httptest.NewRecorder()
	g.relayTunnelHandler(rec, httptest.NewRequest(http.MethodGet, relayPath+"?host=ns-a."+relayBase, nil))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("no upgrade: status %d", rec.Code)
	}
}

// A relay whose Tor is down says so and dials nothing: a direct dial would
// hand the destination the client's relay, which is the opposite of the point.
func TestRelayHandler_torDownIs503AndNothingIsDialled(t *testing.T) {
	var dialled atomic.Int32
	g := relayTestGateway(t, false, func(context.Context, string, string) (net.Conn, error) {
		dialled.Add(1)
		return nil, errors.New("must not be called")
	})
	rec := httptest.NewRecorder()
	g.relayTunnelHandler(rec, relayWSRequest("host=ns-a."+relayBase+"&port=443"))
	wantRelayRefusal(t, rec, http.StatusServiceUnavailable, CodeRelayUnavailable)
	if dialled.Load() != 0 {
		t.Error("the relay dialled with Tor down")
	}
}

func TestRelayHandler_aFailedDialIs503NotAnEchoOfTheCause(t *testing.T) {
	g := relayTestGateway(t, true, func(context.Context, string, string) (net.Conn, error) {
		return nil, errors.New("socks connect tcp 127.0.0.1:9050: connection refused to ns-a." + relayBase)
	})
	rec := httptest.NewRecorder()
	g.relayTunnelHandler(rec, relayWSRequest("host=ns-a."+relayBase+"&port=443"))
	wantRelayRefusal(t, rec, http.StatusServiceUnavailable, CodeRelayUnavailable)
	for _, leak := range []string{"socks", "9050", "refused", relayBase} {
		if strings.Contains(strings.ToLower(rec.Body.String()), leak) {
			t.Errorf("the refusal leaks %q: %s", leak, rec.Body.String())
		}
	}
}

func TestRelayHandler_fullPoolIs429WithRetryAfter(t *testing.T) {
	g := relayTestGateway(t, true, func(context.Context, string, string) (net.Conn, error) { return nil, errors.New("x") })
	for range relayMaxStreams {
		if _, ok := g.relay.acquire(); !ok {
			t.Fatal("setup")
		}
	}
	rec := httptest.NewRecorder()
	g.relayTunnelHandler(rec, relayWSRequest("host=ns-a."+relayBase+"&port=443"))
	wantRelayRefusal(t, rec, http.StatusTooManyRequests, string(httputil.ErrCodeRateLimited))
	if rec.Header().Get("Retry-After") == "" {
		t.Error("no Retry-After")
	}
}

// What the relay carries, and what it logs of it.
func TestRelayHandler_carriesBytesWithFreshCircuitsAndLogsNothingAboutThem(t *testing.T) {
	core, logs := observer.New(zapcore.DebugLevel)
	var keys []string
	gwSide, dest := net.Pipe()
	g := relayTestGateway(t, true, func(_ context.Context, addr, key string) (net.Conn, error) {
		keys = append(keys, key)
		if addr != "ns-a."+relayBase+":443" {
			t.Errorf("dialled %q", addr)
		}
		return gwSide, nil
	})
	g.logger = &logging.ColoredLogger{Logger: zap.New(core)}
	srv := httptest.NewServer(http.HandlerFunc(g.relayTunnelHandler))
	t.Cleanup(srv.Close)

	url := "ws" + strings.TrimPrefix(srv.URL, "http") + relayPath + "?host=ns-a." + relayBase + "&port=443"
	c, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	payload := []byte{0x16, 0x03, 0x01, 0xff, 0x00}
	if err := c.WriteMessage(websocket.BinaryMessage, payload); err != nil {
		t.Fatal(err)
	}
	got := make([]byte, len(payload))
	if _, err := io.ReadFull(dest, got); err != nil || !bytes.Equal(got, payload) {
		t.Fatalf("destination got % x (%v)", got, err)
	}
	_ = dest.Close() // the destination ends: the relay closes the socket
	_ = c.SetReadDeadline(time.Now().Add(5 * time.Second))
	if _, _, err := c.ReadMessage(); err == nil {
		t.Error("the socket stayed open after the destination closed")
	}
	_ = c.Close()

	if len(keys) != 1 || len(keys[0]) != 2*relayIsolationBytes {
		t.Errorf("isolation keys = %v", keys)
	}
	for _, e := range logs.All() {
		t.Errorf("the relay logged %q %v", e.Message, e.Context)
	}
}

func TestRelayIsolationKey_freshPerStreamUnlessTheClientAsksForASession(t *testing.T) {
	g := newTunnelTestGateway(t)
	r := httptest.NewRequest(http.MethodGet, relayPath+"?host=x", nil)
	r.RemoteAddr = "198.51.100.7:4000"
	a, _ := g.relayIsolationKey(r)
	b, _ := g.relayIsolationKey(r)
	if a == b {
		t.Error("two streams share a circuit by default")
	}
	s := httptest.NewRequest(http.MethodGet, relayPath+"?host=x&circuit=session", nil)
	s.RemoteAddr = "198.51.100.7:4001"
	s1, _ := g.relayIsolationKey(s)
	s2, _ := g.relayIsolationKey(s)
	if s1 != s2 {
		t.Error("a session circuit is not stable for one client")
	}
	other := httptest.NewRequest(http.MethodGet, relayPath+"?host=x&circuit=session", nil)
	other.RemoteAddr = "203.0.113.9:4001"
	if o, _ := g.relayIsolationKey(other); o == s1 {
		t.Error("two clients share a session circuit")
	}
}

func TestRelayLimits_arePinnedAndQuiet(t *testing.T) {
	if relayLimits.maxBytes != 64<<20 || relayLimits.maxDuration != 5*time.Minute || !relayLimits.quiet {
		t.Errorf("relay limits = %+v", relayLimits)
	}
	if relayStreamsPerMinute != 30 || relayStreamBurst != 10 {
		t.Error("the per-address bucket is not 30 a minute, burst 10")
	}
}

func TestRelayRateLimit_isPerAddressAndAnswersWithACode(t *testing.T) {
	g := newTunnelTestGateway(t)
	g.rateLimiter = NewRateLimiter(100000, 100000)
	g.relayRateLimiter = NewRateLimiter(relayStreamsPerMinute, relayStreamBurst)
	h := g.rateLimitMiddleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }))

	do := func(addr string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodGet, relayPath, nil)
		r.RemoteAddr = addr
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, r)
		return rec
	}
	for i := range relayStreamBurst {
		if rec := do("198.51.100.7:1"); rec.Code != http.StatusNoContent {
			t.Fatalf("stream %d: status %d", i, rec.Code)
		}
	}
	rec := do("198.51.100.7:2")
	wantRelayRefusal(t, rec, http.StatusTooManyRequests, string(httputil.ErrCodeRateLimited))
	if rec.Header().Get("Retry-After") == "" {
		t.Error("no Retry-After")
	}
	if rec := do("203.0.113.9:1"); rec.Code != http.StatusNoContent {
		t.Errorf("another address was limited: %d", rec.Code)
	}
	// Other paths do not draw on the relay's bucket.
	r := httptest.NewRequest(http.MethodGet, "/v1/health", nil)
	r.RemoteAddr = "198.51.100.7:3"
	rec2 := httptest.NewRecorder()
	h.ServeHTTP(rec2, r)
	if rec2.Code != http.StatusNoContent {
		t.Errorf("a non-relay path was refused: %d", rec2.Code)
	}
}

// The request log row of the relayed download has no address; every other
// route's does. (The relay itself writes no row: relay_logging_test.go.)
func TestRequestLogEntry_relayRoutesKeepNoAddress(t *testing.T) {
	for _, path := range []string{"/v1/storage/relayed/bafy"} {
		r := httptest.NewRequest(http.MethodGet, path, nil)
		r.RemoteAddr = "198.51.100.7:4000"
		policy := gatewayRoutes.For(r)
		if policy.RequestLog == routepolicy.LogFull {
			t.Fatalf("%s declares no reduced request log", path)
		}
		entry := newRequestLogEntry(r, policy, 200, 10, time.Second, "")
		_, args := buildRequestLogInsert([]requestLogEntry{entry}, nil)
		for _, a := range args {
			if s, ok := a.(string); ok && strings.Contains(s, "198.51.100.7") {
				t.Errorf("%s: the INSERT carries the address: %v", path, args)
			}
		}
		if entry.ip != "" {
			t.Errorf("%s: ip = %q", path, entry.ip)
		}
	}

	r := httptest.NewRequest(http.MethodGet, "/v1/storage/get/bafy", nil)
	r.RemoteAddr = "127.0.0.1:1"
	r.Header.Set("X-Forwarded-For", "198.51.100.7")
	policy := gatewayRoutes.For(r)
	if policy.RequestLog != routepolicy.LogFull {
		t.Error("an ordinary route declares a reduced request log")
	}
	if entry := newRequestLogEntry(r, policy, 200, 1, time.Millisecond, ""); entry.ip == "" {
		t.Error("an ordinary route's row lost its address")
	}
	if newRequestLogEntry(r, routepolicy.Policy{RequestLog: routepolicy.LogNoAddress}, 200, 1, 0, "").ip != "" {
		t.Error("the flag does not blank the address")
	}
}

func TestRoutePolicy_relayedFetchRoutes(t *testing.T) {
	relay := policyOf(http.MethodGet, relayPath)
	if !relay.Access.Anonymous() || !relay.MainGateway || relay.RequestLog != routepolicy.LogNone {
		t.Errorf("relay policy = %+v", relay)
	}
	relayed := policyOf(http.MethodGet, "/v1/storage/relayed/")
	if !relayed.Access.Anonymous() || relayed.MainGateway || relayed.RequestLog != routepolicy.LogNoAddress {
		t.Errorf("relayed download policy = %+v", relayed)
	}
	for _, path := range []string{"/v1/storage/fetch-caps", "/v1/storage/fetch-caps/"} {
		p := policyOf(http.MethodPost, path)
		if p.Access.Anonymous() || p.Domain != "storage" || p.Action != "read" || p.Token != routepolicy.PrincipalToken {
			t.Errorf("%s policy = %+v", path, p)
		}
	}
}
