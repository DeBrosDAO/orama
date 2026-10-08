package gateway

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"github.com/DeBrosOfficial/network/pkg/anonproxy"
	"github.com/DeBrosOfficial/network/pkg/httputil"
)

// The anonymous relay of a relayed fetch (bugboard #266).
//
// WHAT THIS IS
//
// GET /v1/proxy/relay is /v1/proxy/tunnel with the identity taken out and the
// destination pinned. A client that wants to download an object without the
// node that serves it learning its address opens this socket on another node
// (the relay), runs TLS to the serving node's namespace host through it, and
// sends the request, capability header included, inside that TLS. The relay
// carries ciphertext; the serving node sees a Tor exit.
//
// FRAMING
//
// The socket is the tunnel's: each binary WebSocket message carries raw TCP
// bytes, in both directions, with no framing of ours. A text frame ends the
// stream. When the destination closes its side the relay closes the socket; when
// the client closes the socket the relay closes the destination. Nothing else
// travels on it.
//
// WHAT IS DIFFERENT FROM THE TUNNEL
//
//   - No credential. The relay is for a client that must not be named, so the
//     only limit it can apply is the client's address, in memory.
//   - The destination is pinned: a host equal to or under one of
//     relay_allowed_suffixes (the cluster's base domain unless configured), port
//     443, an ASCII hostname, never an IP literal. The relay is not an open proxy.
//   - A fresh Tor circuit per stream, so two fetches of one client are not
//     joined at the exit; ?circuit=session opts into one circuit per client
//     address for a batch.
//   - Its own stream pool, so anonymous traffic cannot starve the tunnels.
//   - It keeps no record of a stream: no request_logs row and no access-log
//     line (route policy LogNone), so no address, byte count or duration is
//     written; the request metrics count it by status with no size.
//
// A failure never falls back to a direct connection: the relay exists to keep
// the client's address from the destination, and a direct dial would be the
// opposite.

const (
	// relayPort is the one port the relay reaches.
	relayPort = 443

	// relayMaxBytes caps each direction of one stream. Stored objects are
	// buffered by the serving node, so a stream carries one object.
	relayMaxBytes = 64 << 20 // 64 MiB

	// relayMaxDuration caps one stream's life.
	relayMaxDuration = 5 * time.Minute

	// relayMaxStreams caps concurrent relay streams on a node, apart from the
	// authenticated tunnels' pool.
	relayMaxStreams = 128

	// relayStreamsPerMinute and relayStreamBurst bound the streams one client
	// address may open: a fetch opens one, so this is generous to a real client.
	relayStreamsPerMinute = 30
	relayStreamBurst      = 10

	// relayMaxStreamsPerAddress caps the streams one client address holds open
	// at once. The rate limiter bounds how fast streams open; without this a
	// patient client could hold the whole pool for the stream lifetime.
	relayMaxStreamsPerAddress = 4

	// relayIsolationBytes is the length of the random circuit selector.
	relayIsolationBytes = 16

	// relayPath is the relay's route.
	relayPath = "/v1/proxy/relay"

	relayCircuitParam   = "circuit"
	relayCircuitSession = "session"
)

var relayLimits = tunnelLimits{maxBytes: relayMaxBytes, maxDuration: relayMaxDuration, quiet: true}

// relayService is the relay's state: where it may go, how many streams it
// carries, and how it reaches the anonymity network. The egress is a field so a
// test can show that a relay with no Tor dials nothing.
type relayService struct {
	suffixes []string
	slots    chan struct{}
	running  func() bool
	dial     func(ctx context.Context, addr, isolationKey string) (net.Conn, error)

	mu      sync.Mutex
	perAddr map[string]int
}

func newRelayService(suffixes []string) *relayService {
	return &relayService{
		suffixes: normalizeRelaySuffixes(suffixes),
		slots:    make(chan struct{}, relayMaxStreams),
		running:  anonproxy.Running,
		dial:     anonproxy.DialThrough,
		perAddr:  map[string]int{},
	}
}

// relayAllowedSuffixes is the destination allowlist of a gateway: the configured
// list, or the cluster's own base domain.
func relayAllowedSuffixes(cfg *Config) []string {
	if cfg == nil {
		return nil
	}
	if len(cfg.RelayAllowedSuffixes) > 0 {
		return cfg.RelayAllowedSuffixes
	}
	if cfg.BaseDomain != "" {
		return []string{cfg.BaseDomain}
	}
	return nil
}

// acquire takes a stream slot, or reports the pool full.
func (s *relayService) acquire() (release func(), ok bool) {
	select {
	case s.slots <- struct{}{}:
		return func() { <-s.slots }, true
	default:
		return nil, false
	}
}

// acquireAddress takes one of the streams a client address may hold open, or
// reports that it holds its maximum.
func (s *relayService) acquireAddress(key string) (release func(), ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.perAddr[key] >= relayMaxStreamsPerAddress {
		return nil, false
	}
	s.perAddr[key]++
	return func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.perAddr[key] <= 1 {
			delete(s.perAddr, key)
			return
		}
		s.perAddr[key]--
	}, true
}

// isolationKey is the SOCKS credential that picks the circuit: random per
// stream, so no two streams share one, unless the client asked to share one per
// address for a batch.
func (g *Gateway) relayIsolationKey(r *http.Request) (string, error) {
	if r.URL.Query().Get(relayCircuitParam) == relayCircuitSession {
		return tunnelIsolationKey(g.tunnelIsolationSecret, "relay-session|"+bucketKeyOf(r)), nil
	}
	b := make([]byte, relayIsolationBytes)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func bucketKeyOf(r *http.Request) string {
	client, _ := rateLimitClient(r)
	return bucketKey(client)
}

// relayTunnelHandler serves GET /v1/proxy/relay.
func (g *Gateway) relayTunnelHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "only GET (WebSocket upgrade) is allowed")
		return
	}
	if !isWebSocketUpgrade(r) {
		writeError(w, http.StatusBadRequest, "this endpoint is a WebSocket relay; connect with a WebSocket client")
		return
	}
	relay := g.relay
	target, ok := relay.target(r.URL.Query().Get("host"), r.URL.Query().Get("port"))
	if !ok {
		writeRelayError(w, http.StatusBadRequest, CodeRelayDestinationNotAllowed,
			"the relay reaches only a host under its allowed suffixes, on port 443")
		return
	}
	if !relay.running() {
		writeRelayError(w, http.StatusServiceUnavailable, CodeRelayUnavailable,
			"the anonymity network is not available on this node")
		return
	}
	release, ok := relay.acquire()
	if !ok {
		writeRelayRateLimited(w, "this relay is carrying its maximum number of streams")
		return
	}
	defer release()
	releaseAddr, ok := relay.acquireAddress(bucketKeyOf(r))
	if !ok {
		writeRelayRateLimited(w, "this address already holds the most streams a client may open")
		return
	}
	defer releaseAddr()

	upstream, ok := g.relayDial(w, r, target)
	if !ok {
		return
	}
	conn, err := (&websocket.Upgrader{CheckOrigin: httputil.CheckWebSocketOrigin}).Upgrade(w, r, nil)
	if err != nil {
		_ = upstream.Close()
		return
	}
	g.relayTunnel(conn, upstream, relayLimits)
}

// relayDial opens the stream to target through the anonymity network. It is
// done before the upgrade, so a failure is a status the client can read. The
// destination is not named in the answer or the log.
func (g *Gateway) relayDial(w http.ResponseWriter, r *http.Request, target tunnelTarget) (net.Conn, bool) {
	key, err := g.relayIsolationKey(r)
	if err != nil {
		writeRelayError(w, http.StatusServiceUnavailable, CodeRelayUnavailable, "the relay could not start a stream")
		return nil, false
	}
	dialCtx, cancel := context.WithTimeout(r.Context(), tunnelDialTimeout)
	defer cancel()
	upstream, err := g.relay.dial(dialCtx, target.addr(), key)
	if err != nil {
		writeRelayError(w, http.StatusServiceUnavailable, CodeRelayUnavailable,
			"could not reach the destination through the anonymity network")
		return nil, false
	}
	return upstream, true
}
