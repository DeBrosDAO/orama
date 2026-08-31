package gateway

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/DeBrosOfficial/network/pkg/gateway/auth"
	"github.com/DeBrosOfficial/network/pkg/logging"
)

// Bugboard #168 — authenticated tunnelling proxy.
//
// The tunnel carries an opaque TCP stream on behalf of an authenticated user,
// which makes it a far more attractive abuse target than the request/response
// anon proxy: an unchecked destination turns every node into an open relay and
// an SSRF probe into the WireGuard mesh. These cover the gate, not the plumbing.

// --- destination validation ---------------------------------------------------

func TestParseTunnelTarget_acceptsPublicWebDestinations(t *testing.T) {
	for _, tc := range []struct {
		host, port string
		wantPort   int
	}{
		{"example.com", "443", 443},
		{"example.com", "80", 80},
		{"example.com", "", 443}, // port defaults to https
		{"sub.domain.example.com", "443", 443},
		{"1.1.1.1", "443", 443},
		{"[2606:4700:4700::1111]", "443", 443},
	} {
		t.Run(tc.host+":"+tc.port, func(t *testing.T) {
			got, err := parseTunnelTarget(tc.host, tc.port)
			if err != nil {
				t.Fatalf("parseTunnelTarget(%q,%q): %v", tc.host, tc.port, err)
			}
			if got.port != tc.wantPort {
				t.Errorf("port = %d, want %d", got.port, tc.wantPort)
			}
		})
	}
}

// Every one of these would otherwise let an authenticated user aim a tunnel at
// infrastructure: the WireGuard mesh (10.0.0.x), the node's own services on
// loopback, or a cloud metadata endpoint on 169.254.169.254.
func TestParseTunnelTarget_rejectsNonPublicDestinations(t *testing.T) {
	for _, host := range []string{
		"127.0.0.1",
		"localhost",
		"api.localhost",
		"LOCALHOST",
		"10.0.0.1",      // WireGuard mesh
		"10.0.0.17",     // a real devnet node's internal IP
		"192.168.1.1",
		"172.16.0.1",
		"169.254.169.254", // cloud metadata
		"100.100.0.1",     // carrier-grade NAT
		"0.0.0.0",
		"[::1]",
		"[fe80::1]",
		"224.0.0.1", // multicast
	} {
		t.Run(host, func(t *testing.T) {
			if _, err := parseTunnelTarget(host, "443"); err == nil {
				t.Errorf("parseTunnelTarget(%q) was accepted; it must be refused", host)
			}
		})
	}
}

// The port allowlist is what stops the tunnel being a general-purpose relay to
// mail submission, databases and internal admin panels.
func TestParseTunnelTarget_rejectsPortsOutsideTheAllowlist(t *testing.T) {
	for _, port := range []string{"22", "25", "587", "3306", "5432", "6379", "5001", "6001", "9050", "0", "65536", "-1", "http"} {
		t.Run(port, func(t *testing.T) {
			if _, err := parseTunnelTarget("example.com", port); err == nil {
				t.Errorf("port %q was accepted; only 80 and 443 are permitted", port)
			}
		})
	}
}

func TestParseTunnelTarget_rejectsMalformedHosts(t *testing.T) {
	for name, host := range map[string]string{
		"empty":            "",
		"whitespace only":  "   ",
		"embedded space":   "exa mple.com",
		"embedded newline": "example.com\r\nHost: evil",
		"path smuggled":    "example.com/../admin",
		"userinfo":         "user@example.com",
		"too long":         strings.Repeat("a", 254),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := parseTunnelTarget(host, "443"); err == nil {
				t.Errorf("host %q was accepted", host)
			}
		})
	}
}

func TestTunnelTarget_addrRoundTripsIPv6(t *testing.T) {
	got, err := parseTunnelTarget("[2606:4700:4700::1111]", "443")
	if err != nil {
		t.Fatalf("parseTunnelTarget: %v", err)
	}
	if want := "[2606:4700:4700::1111]:443"; got.addr() != want {
		t.Errorf("addr() = %q, want %q", got.addr(), want)
	}
}

// --- concurrency limits -------------------------------------------------------

func TestTunnelLimiter_capsPerUserAndReleases(t *testing.T) {
	l := newTunnelLimiter()
	releases := make([]func(), 0, tunnelMaxPerUser)
	for i := 0; i < tunnelMaxPerUser; i++ {
		rel, err := l.acquire("alice")
		if err != nil {
			t.Fatalf("acquire %d: %v", i, err)
		}
		releases = append(releases, rel)
	}
	if _, err := l.acquire("alice"); err == nil {
		t.Fatal("one user must not exceed the per-user cap")
	}
	// A different user is unaffected — the cap is per user, not global-by-proxy.
	relBob, err := l.acquire("bob")
	if err != nil {
		t.Fatalf("a second user must still be admitted: %v", err)
	}
	relBob()

	releases[0]()
	if _, err := l.acquire("alice"); err != nil {
		t.Fatalf("a released slot must be reusable: %v", err)
	}
}

// A double release would decrement the counters twice and eventually let a user
// exceed the cap, or drive the total negative.
func TestTunnelLimiter_releaseIsIdempotent(t *testing.T) {
	l := newTunnelLimiter()
	rel, err := l.acquire("alice")
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	rel()
	rel()
	rel()

	l.mu.Lock()
	total, perUser := l.total, l.perUser["alice"]
	l.mu.Unlock()
	if total != 0 {
		t.Errorf("total = %d after repeated release, want 0", total)
	}
	if perUser != 0 {
		t.Errorf("perUser = %d after repeated release, want 0", perUser)
	}
}

// The per-user map is keyed by caller identity; leaving zero entries behind
// would grow it without bound over a node's lifetime.
func TestTunnelLimiter_doesNotLeakUserEntries(t *testing.T) {
	l := newTunnelLimiter()
	for i := 0; i < 500; i++ {
		rel, err := l.acquire(fmt.Sprintf("user-%d", i))
		if err != nil {
			t.Fatalf("acquire: %v", err)
		}
		rel()
	}
	l.mu.Lock()
	n := len(l.perUser)
	l.mu.Unlock()
	if n != 0 {
		t.Errorf("perUser retains %d entries after every tunnel closed, want 0", n)
	}
}

func TestTunnelLimiter_capsNodeTotal(t *testing.T) {
	l := newTunnelLimiter()
	// Spread across users so the per-user cap is never the binding constraint.
	perUser := tunnelMaxPerUser
	users := tunnelMaxTotal/perUser + 1
	granted := 0
	for u := 0; u < users; u++ {
		for i := 0; i < perUser; i++ {
			if _, err := l.acquire(fmt.Sprintf("u%d", u)); err != nil {
				break
			}
			granted++
		}
	}
	if granted != tunnelMaxTotal {
		t.Errorf("granted %d tunnels, want the node cap of %d", granted, tunnelMaxTotal)
	}
	if _, err := l.acquire("someone-else"); err == nil {
		t.Fatal("the node-wide cap must refuse further tunnels")
	}
}

func TestTunnelLimiter_concurrentAcquireReleaseIsRaceFree(t *testing.T) {
	l := newTunnelLimiter()
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				if rel, err := l.acquire(fmt.Sprintf("u%d", i%5)); err == nil {
					rel()
				}
			}
		}(i)
	}
	wg.Wait()
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.total != 0 {
		t.Errorf("total = %d after all tunnels closed, want 0", l.total)
	}
}

// --- circuit isolation --------------------------------------------------------

// Distinct users must land on distinct circuits: without this every tunnel on a
// node shares one circuit and one exit, making users linkable to each other.
func TestTunnelIsolationKey_differsPerUserAndIsStable(t *testing.T) {
	const secret = "cluster-secret|12D3KooWnode"
	alice := tunnelIsolationKey(secret, "0xAlice")
	bob := tunnelIsolationKey(secret, "0xBob")

	if alice == bob {
		t.Fatal("two users must not share a circuit selector")
	}
	if alice != tunnelIsolationKey(secret, "0xAlice") {
		t.Error("the selector must be stable for a user, or every tunnel builds a new circuit")
	}
	// The same user on a DIFFERENT node must not produce the same selector, or
	// the anonymity network gains a cross-node correlator for that user.
	if alice == tunnelIsolationKey("other-secret|12D3KooWother", "0xAlice") {
		t.Error("the selector must be node-local")
	}
}

// The selector is handed to the local SOCKS port. It must not be, or contain,
// the user's wallet address.
func TestTunnelIsolationKey_doesNotLeakTheIdentity(t *testing.T) {
	const wallet = "0x1234567890abcdef1234567890abcdef12345678"
	key := tunnelIsolationKey("secret", wallet)
	if strings.Contains(strings.ToLower(key), strings.ToLower(wallet)) {
		t.Fatal("the circuit selector must not contain the caller's identity")
	}
	if strings.Contains(strings.ToLower(key), strings.ToLower(wallet[2:10])) {
		t.Fatal("the circuit selector must not contain a prefix of the caller's identity")
	}
	if key == "" {
		t.Fatal("the selector must not be empty — that would disable isolation")
	}
}

func TestTunnelSecretFrom_neverEmpty(t *testing.T) {
	if got := tunnelSecretFrom(nil); got == "" {
		t.Error("a nil config must still yield a usable secret")
	}
	if got := tunnelSecretFrom(&Config{}); got == "" {
		t.Error("an empty config must still yield a usable secret")
	}
	withNode := tunnelSecretFrom(&Config{ClusterSecret: "cs", NodePeerID: "peer"})
	if !strings.Contains(withNode, "cs") || !strings.Contains(withNode, "peer") {
		t.Errorf("secret = %q, want it derived from both the cluster secret and the peer ID", withNode)
	}
}

// --- caller identity ----------------------------------------------------------

// An API-key-exchanged JWT is not an end user. Accepting one would mean an
// extracted app-runtime key could open tunnels, which is exactly the posture
// this endpoint must not have.
func TestTunnelCallerIdentity_rejectsNonWalletCredentials(t *testing.T) {
	for name, claims := range map[string]*auth.JWTClaims{
		"api-key subject": {Sub: "ak_abc123:myns"},
		"empty subject":   {Sub: "   "},
	} {
		t.Run(name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "/v1/proxy/tunnel", nil)
			r = r.WithContext(context.WithValue(r.Context(), ctxKeyJWT, claims))
			if got := tunnelCallerIdentity(r); got != "" {
				t.Errorf("identity = %q, want empty", got)
			}
		})
	}

	t.Run("no credentials at all", func(t *testing.T) {
		r := httptest.NewRequest(http.MethodGet, "/v1/proxy/tunnel", nil)
		if got := tunnelCallerIdentity(r); got != "" {
			t.Errorf("identity = %q, want empty", got)
		}
	})

	t.Run("wallet subject is accepted", func(t *testing.T) {
		r := httptest.NewRequest(http.MethodGet, "/v1/proxy/tunnel", nil)
		r = r.WithContext(context.WithValue(r.Context(), ctxKeyJWT,
			&auth.JWTClaims{Sub: "0xabc"}))
		if got := tunnelCallerIdentity(r); got != "0xabc" {
			t.Errorf("identity = %q, want 0xabc", got)
		}
	})
}

// --- handler gate -------------------------------------------------------------

func mustTunnelTestLogger(t *testing.T) *logging.ColoredLogger {
	t.Helper()
	l, err := logging.NewColoredLogger(logging.ComponentGeneral, false)
	if err != nil {
		t.Fatalf("logger: %v", err)
	}
	return l
}

func newTunnelTestGateway(t *testing.T) *Gateway {
	t.Helper()
	return &Gateway{
		logger:                mustTunnelTestLogger(t),
		tunnelLimiter:         newTunnelLimiter(),
		tunnelIsolationSecret: "test-secret",
	}
}

// Everything the handler refuses must be refused BEFORE the WebSocket upgrade,
// so the client reads a real HTTP status instead of a socket that opens and
// closes for reasons most clients surface poorly.
func TestAnonTunnelHandler_refusesBeforeUpgrade(t *testing.T) {
	walletCtx := func(r *http.Request) *http.Request {
		return r.WithContext(context.WithValue(r.Context(), ctxKeyJWT, &auth.JWTClaims{Sub: "0xabc"}))
	}

	for _, tc := range []struct {
		name     string
		build    func() *http.Request
		wantCode int
	}{
		{
			name: "non-GET",
			build: func() *http.Request {
				return walletCtx(httptest.NewRequest(http.MethodPost, "/v1/proxy/tunnel?host=example.com", nil))
			},
			wantCode: http.StatusMethodNotAllowed,
		},
		{
			name: "plain GET without an upgrade",
			build: func() *http.Request {
				return walletCtx(httptest.NewRequest(http.MethodGet, "/v1/proxy/tunnel?host=example.com", nil))
			},
			wantCode: http.StatusBadRequest,
		},
		{
			name: "upgrade without a wallet JWT",
			build: func() *http.Request {
				r := httptest.NewRequest(http.MethodGet, "/v1/proxy/tunnel?host=example.com", nil)
				r.Header.Set("Connection", "Upgrade")
				r.Header.Set("Upgrade", "websocket")
				return r
			},
			wantCode: http.StatusUnauthorized,
		},
		{
			name: "upgrade with an api-key JWT",
			build: func() *http.Request {
				r := httptest.NewRequest(http.MethodGet, "/v1/proxy/tunnel?host=example.com", nil)
				r.Header.Set("Connection", "Upgrade")
				r.Header.Set("Upgrade", "websocket")
				return r.WithContext(context.WithValue(r.Context(), ctxKeyJWT, &auth.JWTClaims{Sub: "ak_k:ns"}))
			},
			wantCode: http.StatusUnauthorized,
		},
		{
			name: "upgrade to a private destination",
			build: func() *http.Request {
				r := httptest.NewRequest(http.MethodGet, "/v1/proxy/tunnel?host=10.0.0.1&port=443", nil)
				r.Header.Set("Connection", "Upgrade")
				r.Header.Set("Upgrade", "websocket")
				return walletCtx(r)
			},
			wantCode: http.StatusBadRequest,
		},
		{
			name: "upgrade to a forbidden port",
			build: func() *http.Request {
				r := httptest.NewRequest(http.MethodGet, "/v1/proxy/tunnel?host=example.com&port=22", nil)
				r.Header.Set("Connection", "Upgrade")
				r.Header.Set("Upgrade", "websocket")
				return walletCtx(r)
			},
			wantCode: http.StatusBadRequest,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			newTunnelTestGateway(t).anonTunnelHandler(rec, tc.build())
			if rec.Code != tc.wantCode {
				t.Errorf("status = %d, want %d (body: %s)", rec.Code, tc.wantCode, rec.Body.String())
			}
		})
	}
}

// A refused request must not consume a limiter slot, or a client probing
// forbidden destinations would exhaust its own quota (and the node's).
func TestAnonTunnelHandler_refusalDoesNotConsumeASlot(t *testing.T) {
	g := newTunnelTestGateway(t)
	for i := 0; i < tunnelMaxPerUser*3; i++ {
		r := httptest.NewRequest(http.MethodGet, "/v1/proxy/tunnel?host=10.0.0.1&port=443", nil)
		r.Header.Set("Connection", "Upgrade")
		r.Header.Set("Upgrade", "websocket")
		r = r.WithContext(context.WithValue(r.Context(), ctxKeyJWT, &auth.JWTClaims{Sub: "0xabc"}))
		g.anonTunnelHandler(httptest.NewRecorder(), r)
	}
	g.tunnelLimiter.mu.Lock()
	defer g.tunnelLimiter.mu.Unlock()
	if g.tunnelLimiter.total != 0 {
		t.Errorf("%d slots held after only refused requests, want 0", g.tunnelLimiter.total)
	}
}

// The error returned for an unreachable destination must not describe what went
// wrong: a tunnel that reports exactly why a dial failed is a probe for
// whatever the exit relay can reach.
func TestAnonTunnelHandler_dialErrorIsNotEchoed(t *testing.T) {
	// Bind a listener and immediately close it, so we have an address nothing
	// answers on — the SOCKS connect will fail deterministically.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	_ = ln.Close()

	g := newTunnelTestGateway(t)
	r := httptest.NewRequest(http.MethodGet, "/v1/proxy/tunnel?host=example.com&port=443", nil)
	r.Header.Set("Connection", "Upgrade")
	r.Header.Set("Upgrade", "websocket")
	r = r.WithContext(context.WithValue(r.Context(), ctxKeyJWT, &auth.JWTClaims{Sub: "0xabc"}))

	rec := httptest.NewRecorder()
	g.anonTunnelHandler(rec, r)

	body := strings.ToLower(rec.Body.String())
	// Whether the anonymity client happens to be running locally decides between
	// 503 and 502; either way the body must stay generic.
	if rec.Code != http.StatusServiceUnavailable && rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502 or 503 (body: %s)", rec.Code, rec.Body.String())
	}
	for _, leak := range []string{"socks", "refused", "dial tcp", "127.0.0.1", "example.com"} {
		if strings.Contains(body, leak) {
			t.Errorf("response leaks %q to the caller: %s", leak, rec.Body.String())
		}
	}
}

// --- relay ---------------------------------------------------------------------

// relayHarness stands up a real WebSocket server whose handler splices the
// socket to a stub "destination" over net.Pipe, then dials it with a real
// WebSocket client. Nothing is mocked on the path under test.
type relayHarness struct {
	client *websocket.Conn
	dest   net.Conn // the stub destination's end of the pipe
	done   chan struct{}
	sent   int64
	recvd  int64
}

func newRelayHarness(t *testing.T) *relayHarness {
	t.Helper()
	g := newTunnelTestGateway(t)
	gwSide, destSide := net.Pipe()
	h := &relayHarness{dest: destSide, done: make(chan struct{})}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade: %v", err)
			return
		}
		h.sent, h.recvd = g.relayTunnel(conn, gwSide)
		close(h.done)
	}))
	t.Cleanup(srv.Close)

	c, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(srv.URL, "http"), nil)
	if err != nil {
		t.Fatalf("dial websocket: %v", err)
	}
	h.client = c
	t.Cleanup(func() { _ = c.Close() })
	return h
}

// The core guarantee: bytes cross verbatim in both directions. The tunnel adds
// no framing of its own, because anything it added would have to be understood
// by the client's TLS stack — which is the whole point of the design.
func TestRelayTunnel_carriesBytesVerbatimBothWays(t *testing.T) {
	h := newRelayHarness(t)

	// client → destination. Use bytes that would be mangled by any text or
	// line-oriented handling; a TLS record is binary.
	payload := []byte{0x16, 0x03, 0x01, 0x00, 0x00, 0xff, 0x0a, 0x0d, 0x00}
	if err := h.client.WriteMessage(websocket.BinaryMessage, payload); err != nil {
		t.Fatalf("client write: %v", err)
	}
	got := make([]byte, len(payload))
	if _, err := io.ReadFull(h.dest, got); err != nil {
		t.Fatalf("destination read: %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Errorf("destination received % x, want % x", got, payload)
	}

	// destination → client
	reply := []byte{0x17, 0x03, 0x03, 0x00, 0x01, 0x00, 0xfe}
	go func() { _, _ = h.dest.Write(reply) }()
	msgType, back, err := h.client.ReadMessage()
	if err != nil {
		t.Fatalf("client read: %v", err)
	}
	if msgType != websocket.BinaryMessage {
		t.Errorf("message type = %d, want binary", msgType)
	}
	if !bytes.Equal(back, reply) {
		t.Errorf("client received % x, want % x", back, reply)
	}

	_ = h.client.Close()
	<-h.done
}

// Closing either end must tear down the other. A tunnel whose upstream survives
// its WebSocket leaks a socket and a circuit per abandoned connection.
func TestRelayTunnel_clientCloseTearsDownUpstream(t *testing.T) {
	h := newRelayHarness(t)
	_ = h.client.Close()

	select {
	case <-h.done:
	case <-time.After(5 * time.Second):
		t.Fatal("relay did not return after the client closed")
	}
	if _, err := h.dest.Read(make([]byte, 1)); err == nil {
		t.Error("upstream connection is still open after the client closed")
	}
}

func TestRelayTunnel_upstreamCloseTearsDownClient(t *testing.T) {
	h := newRelayHarness(t)
	_ = h.dest.Close()

	select {
	case <-h.done:
	case <-time.After(5 * time.Second):
		t.Fatal("relay did not return after the destination closed")
	}
	_ = h.client.SetReadDeadline(time.Now().Add(5 * time.Second))
	if _, _, err := h.client.ReadMessage(); err == nil {
		t.Error("client socket is still open after the destination closed")
	}
}

// The tunnel carries opaque bytes. A text frame means the peer is speaking some
// other protocol; relaying its payload would corrupt the stream silently, so
// the tunnel is torn down instead.
func TestRelayTunnel_textFrameClosesTheTunnel(t *testing.T) {
	h := newRelayHarness(t)
	if err := h.client.WriteMessage(websocket.TextMessage, []byte("hello")); err != nil {
		t.Fatalf("client write: %v", err)
	}
	select {
	case <-h.done:
	case <-time.After(5 * time.Second):
		t.Fatal("a text frame must close the tunnel, not be relayed")
	}
	if _, err := h.dest.Read(make([]byte, 1)); err == nil {
		t.Error("upstream still open after a protocol violation")
	}
}

// Byte accounting is what the operator sees in the close log and what the caps
// are enforced against; it must reflect what actually crossed.
func TestRelayTunnel_reportsBytesCarried(t *testing.T) {
	h := newRelayHarness(t)

	up := []byte("0123456789")
	if err := h.client.WriteMessage(websocket.BinaryMessage, up); err != nil {
		t.Fatalf("client write: %v", err)
	}
	if _, err := io.ReadFull(h.dest, make([]byte, len(up))); err != nil {
		t.Fatalf("destination read: %v", err)
	}

	down := []byte("abcdefg")
	go func() { _, _ = h.dest.Write(down) }()
	if _, _, err := h.client.ReadMessage(); err != nil {
		t.Fatalf("client read: %v", err)
	}

	_ = h.client.Close()
	<-h.done

	if h.sent != int64(len(up)) {
		t.Errorf("bytes to destination = %d, want %d", h.sent, len(up))
	}
	if h.recvd != int64(len(down)) {
		t.Errorf("bytes to client = %d, want %d", h.recvd, len(down))
	}
}
