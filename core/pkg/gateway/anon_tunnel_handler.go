package gateway

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"github.com/DeBrosOfficial/network/pkg/anyoneproxy"
	"github.com/DeBrosOfficial/network/pkg/gateway/auth"
	"github.com/DeBrosOfficial/network/pkg/httputil"
	"github.com/DeBrosOfficial/network/pkg/logging"
	"go.uber.org/zap"
)

// Authenticated tunnelling proxy through the anonymity network (bugboard #168).
//
// WHAT THIS IS
//
// `/v1/proxy/anon` proxies one HTTP request at a time: the caller hands the
// gateway a URL, headers and a body, and the gateway performs the request. That
// hides the end user's IP from the destination, but it necessarily puts the
// gateway inside the TLS boundary — every URL, header and byte passes through
// it in cleartext. For general web browsing that turns the gateway into a
// complete browsing-history observer.
//
// This endpoint carries an opaque TCP stream instead. The client negotiates TLS
// end-to-end with the destination THROUGH the tunnel, so the gateway relays
// ciphertext and never sees a path, a header or a body.
//
// WHAT THE GATEWAY STILL SEES, AND WHY IT CANNOT BE OTHERWISE
//
// The destination HOST and PORT. They are the tunnel's addressing information —
// the gateway cannot dial a connection without them. Every tunnelling proxy has
// this property. The guarantee this endpoint makes is precise: the destination
// never learns the user's IP, and the gateway never learns anything about the
// traffic beyond which host it is going to. Callers must not describe it to
// their users as hiding the sites they visit from us.
//
// WHY WEBSOCKET RATHER THAN HTTP CONNECT
//
// A CONNECT listener would be the conventional shape, and a mobile web view can
// be pointed at a CONNECT proxy directly. It is not reachable here: nodes serve
// :443 through Caddy's reverse_proxy, which does not forward the CONNECT method
// to a backend, and the only open ports are 22/53/80/443/51820 plus the TURN
// range. Reaching a CONNECT listener would mean either enabling the SNI router
// (per-node opt-in, currently disabled everywhere) or opening a new port on
// every node — both of which change the install surface for every operator.
//
// A WebSocket reaches the gateway today, over the existing port, with the
// existing certificate, through the existing authentication. Clients that need
// a real proxy endpoint run a loopback CONNECT relay that maps each local
// CONNECT onto one tunnel — which mobile clients already need regardless, since
// one of the two platforms cannot attach credentials to a web view's proxy.
//
// AUTHENTICATION
//
// Per USER, not per app: the path is under /v1/proxy/, so scopeMiddleware
// requires the `proxy` grant AND a genuine wallet JWT. An extracted app-runtime
// key is not sufficient, and cannot be — an authenticated tunnel is a far more
// attractive abuse target than a request proxy.

const (
	// tunnelIdleTimeout closes a tunnel that has carried no bytes in either
	// direction for this long. Browsers keep connections open speculatively;
	// without this a node accumulates idle tunnels until it runs out of file
	// descriptors.
	tunnelIdleTimeout = 2 * time.Minute

	// tunnelMaxDuration caps a single tunnel's lifetime regardless of activity.
	// A long-lived tunnel is a long-lived circuit; recycling bounds how much
	// traffic any one circuit carries.
	tunnelMaxDuration = 30 * time.Minute

	// tunnelMaxBytes caps bytes relayed in one tunnel (each direction counted
	// separately). Nodes pay for this bandwidth, so a single stream must not be
	// able to consume a node's transfer allowance.
	tunnelMaxBytes = 256 << 20 // 256 MiB

	// tunnelMaxPerUser caps concurrent tunnels for one user across this node.
	// A browser opens several connections per page, so this cannot be small;
	// it exists to stop one user monopolising the node.
	tunnelMaxPerUser = 24

	// tunnelMaxTotal caps concurrent tunnels on this node across all users.
	tunnelMaxTotal = 512

	// tunnelDialTimeout bounds the SOCKS connect to the destination. Circuits
	// through an anonymity network are slow to build; too short a timeout makes
	// the tunnel unusable rather than safe.
	tunnelDialTimeout = 30 * time.Second

	// tunnelWriteTimeout bounds a single WebSocket write, so a client that
	// stops reading cannot pin a relay goroutine and its buffers forever.
	tunnelWriteTimeout = 30 * time.Second

	// tunnelReadBuffer is the chunk size for destination → client relaying.
	tunnelReadBuffer = 32 << 10
)

// tunnelAllowedPorts is the set of destination ports a tunnel may reach.
//
// A tunnel accepts an arbitrary host:port from the client, which makes it an
// SSRF primitive if left open: mail submission, database ports and internal
// admin panels all become reachable from a node's exit. Web browsing — the
// entire purpose of this endpoint — needs exactly these two.
var tunnelAllowedPorts = map[int]struct{}{
	80:  {},
	443: {},
}

// tunnelLimiter bounds concurrent tunnels per user and per node.
type tunnelLimiter struct {
	mu      sync.Mutex
	perUser map[string]int
	total   int
}

func newTunnelLimiter() *tunnelLimiter {
	return &tunnelLimiter{perUser: make(map[string]int)}
}

// acquire reserves a slot. The returned release func is safe to call once.
func (l *tunnelLimiter) acquire(user string) (func(), error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.total >= tunnelMaxTotal {
		return nil, fmt.Errorf("this node is carrying its maximum of %d tunnels", tunnelMaxTotal)
	}
	if l.perUser[user] >= tunnelMaxPerUser {
		return nil, fmt.Errorf("you already have the maximum of %d concurrent tunnels", tunnelMaxPerUser)
	}
	l.perUser[user]++
	l.total++

	var once sync.Once
	return func() {
		once.Do(func() {
			l.mu.Lock()
			defer l.mu.Unlock()
			l.total--
			if n := l.perUser[user] - 1; n > 0 {
				l.perUser[user] = n
			} else {
				// Drop the key rather than leaving a zero entry: the map is
				// keyed by user and would otherwise grow without bound.
				delete(l.perUser, user)
			}
		})
	}, nil
}

// tunnelTarget is a validated destination.
type tunnelTarget struct {
	host string
	port int
}

func (t tunnelTarget) addr() string { return net.JoinHostPort(t.host, strconv.Itoa(t.port)) }

// parseTunnelTarget validates the client-supplied destination.
//
// The host is NOT resolved here. Resolution happens at the exit relay (the SOCKS
// proxy is handed the name verbatim), which is both the privacy-correct place
// for it and the reason a name that resolves to a private address cannot be used
// to reach this node's own network. What is rejected here is a destination
// written as a literal private/loopback/link-local address, which would
// otherwise be a direct request to aim a tunnel at the WireGuard mesh.
func parseTunnelTarget(rawHost, rawPort string) (tunnelTarget, error) {
	host := strings.TrimSpace(rawHost)
	if host == "" {
		return tunnelTarget{}, errors.New("host is required")
	}
	// A bracketed IPv6 literal arrives with its brackets; strip them so the
	// address parses and so JoinHostPort re-adds exactly one pair.
	host = strings.TrimSuffix(strings.TrimPrefix(host, "["), "]")
	if len(host) > 253 {
		return tunnelTarget{}, errors.New("host is too long")
	}
	if strings.ContainsAny(host, " \t\r\n/\\?#@") {
		return tunnelTarget{}, errors.New("host contains invalid characters")
	}
	if strings.EqualFold(host, "localhost") || strings.HasSuffix(strings.ToLower(host), ".localhost") {
		return tunnelTarget{}, errors.New("destination is not permitted")
	}
	if ip := net.ParseIP(host); ip != nil && !isPublicIP(ip) {
		return tunnelTarget{}, errors.New("destination is not permitted")
	}

	if strings.TrimSpace(rawPort) == "" {
		rawPort = "443"
	}
	port, err := strconv.Atoi(strings.TrimSpace(rawPort))
	if err != nil {
		return tunnelTarget{}, fmt.Errorf("port %q is not a number", rawPort)
	}
	if _, ok := tunnelAllowedPorts[port]; !ok {
		return tunnelTarget{}, fmt.Errorf("port %d is not permitted (allowed: 80, 443)", port)
	}
	return tunnelTarget{host: host, port: port}, nil
}

// isPublicIP reports whether an IP is routable on the public internet. Anything
// else — loopback, RFC1918, link-local, multicast, unspecified, and the IPv6
// unique-local range that the WireGuard mesh and cloud metadata services live
// in — is refused as a tunnel destination.
func isPublicIP(ip net.IP) bool {
	if ip == nil || ip.IsLoopback() || ip.IsPrivate() || ip.IsUnspecified() ||
		ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsInterfaceLocalMulticast() ||
		ip.IsMulticast() {
		return false
	}
	// 100.64.0.0/10 (carrier-grade NAT) is not covered by IsPrivate and is
	// where several hosting providers put internal addressing.
	if ip4 := ip.To4(); ip4 != nil && ip4[0] == 100 && ip4[1]&0xc0 == 64 {
		return false
	}
	return true
}

// tunnelIsolationKey derives the SOCKS credential that pins a user to their own
// circuit. It is an HMAC of the caller identity under the node's own key, so the
// value handed to the anonymity client is stable per user (one circuit each,
// reused across their tunnels) yet reveals nothing about who they are — the
// wallet address itself must never leave this process toward the proxy.
func tunnelIsolationKey(secret, identity string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(identity))
	return hex.EncodeToString(mac.Sum(nil))[:16]
}

// tunnelSecretFrom derives the node-local HMAC key used to turn a caller
// identity into a circuit selector.
//
// It must be stable across restarts (otherwise every user is re-shuffled onto a
// fresh circuit whenever the gateway restarts) and node-local (otherwise the
// same user maps to the same selector on every node, which hands the anonymity
// network a cross-node correlator). The cluster secret plus this node's peer ID
// satisfies both. Neither value is exposed: only the HMAC output leaves this
// process, and only to the loopback SOCKS port.
//
// When neither is configured the selector degrades to a fixed per-node value —
// still isolating this node from others, but no longer isolating users from one
// another. That is the pre-existing behaviour of the anon proxy, so it is a
// deliberate floor rather than a failure, and it is logged by the caller.
func tunnelSecretFrom(cfg *Config) string {
	if cfg == nil {
		return "orama-tunnel"
	}
	secret := strings.TrimSpace(cfg.ClusterSecret) + "|" + strings.TrimSpace(cfg.NodePeerID)
	if strings.TrimSpace(strings.Trim(secret, "|")) == "" {
		return "orama-tunnel"
	}
	return secret
}

// tunnelCallerIdentity resolves the authenticated end user for a tunnel.
//
// Only a verified wallet-JWT subject counts. scopeMiddleware has already
// rejected anything else on this path, so an empty result here means the
// middleware was bypassed and the request must not proceed.
func tunnelCallerIdentity(r *http.Request) string {
	if v := r.Context().Value(ctxKeyJWT); v != nil {
		if claims, ok := v.(*auth.JWTClaims); ok && claims != nil {
			// An API-key-exchanged JWT is not an end user. hasWalletJWT already
			// rejects it upstream; repeating the check here keeps the identity
			// this function returns meaningful on its own.
			if isAPIKeySubject(claims.Sub) {
				return ""
			}
			return strings.TrimSpace(claims.Sub)
		}
	}
	return ""
}

// anonTunnelHandler serves GET /v1/proxy/tunnel — a WebSocket carrying one
// opaque TCP stream to `?host=&port=` through the anonymity network.
//
// Frames are relayed verbatim in both directions: binary WebSocket messages
// become TCP payload and vice versa. No framing, length prefix or protocol of
// our own is imposed, because anything we added would have to be understood by
// the client's TLS stack, which is the whole point of the design.
func (g *Gateway) anonTunnelHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "only GET (WebSocket upgrade) is allowed")
		return
	}
	if !isWebSocketUpgrade(r) {
		writeError(w, http.StatusBadRequest,
			"this endpoint is a WebSocket tunnel; connect with a WebSocket client")
		return
	}

	identity := tunnelCallerIdentity(r)
	if identity == "" {
		// Defence in depth: /v1/proxy/ already requires a wallet JWT. Reaching
		// here without one means the auth chain changed shape, and a tunnel is
		// the last thing that should fail open.
		unauthorized(w, CodeAuthUserJWTRequired, "an authenticated user is required for the tunnel", nil)
		return
	}

	target, err := parseTunnelTarget(r.URL.Query().Get("host"), r.URL.Query().Get("port"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	if !anyoneproxy.Running() {
		g.logger.ComponentWarn(logging.ComponentGeneral, "tunnel refused: anonymity proxy not available",
			zap.String("socks_addr", anyoneproxy.Address()))
		writeError(w, http.StatusServiceUnavailable,
			fmt.Sprintf("anonymity network not available at %s", anyoneproxy.Address()))
		return
	}

	release, err := g.tunnelLimiter.acquire(identity)
	if err != nil {
		w.Header().Set("Retry-After", "5")
		writeError(w, http.StatusTooManyRequests, err.Error())
		return
	}

	// Dial BEFORE upgrading: a failure is then a plain HTTP status the client
	// can read, instead of a WebSocket that opens and immediately closes with a
	// reason most clients surface poorly.
	dialCtx, cancelDial := context.WithTimeout(r.Context(), tunnelDialTimeout)
	upstream, dialErr := anyoneproxy.DialThrough(dialCtx, target.addr(),
		tunnelIsolationKey(g.tunnelIsolationSecret, identity))
	cancelDial()
	if dialErr != nil {
		release()
		g.logger.ComponentWarn(logging.ComponentGeneral, "tunnel dial failed",
			zap.String("host", target.host), zap.Int("port", target.port), zap.Error(dialErr))
		// The destination and the reason are not echoed back: a tunnel that
		// reports exactly why a dial failed is a probe for whatever the exit
		// can reach.
		writeError(w, http.StatusBadGateway, "could not reach the destination through the anonymity network")
		return
	}

	conn, err := (&websocket.Upgrader{CheckOrigin: httputil.CheckWebSocketOrigin}).Upgrade(w, r, nil)
	if err != nil {
		_ = upstream.Close()
		release()
		// Upgrade has already written its own error response.
		g.logger.ComponentWarn(logging.ComponentGeneral, "tunnel upgrade failed", zap.Error(err))
		return
	}

	g.logger.ComponentInfo(logging.ComponentGeneral, "tunnel opened",
		zap.String("host", target.host), zap.Int("port", target.port))

	start := time.Now()
	sent, received := g.relayTunnel(conn, upstream)
	release()

	g.logger.ComponentInfo(logging.ComponentGeneral, "tunnel closed",
		zap.String("host", target.host),
		zap.Int("port", target.port),
		zap.Int64("bytes_to_destination", sent),
		zap.Int64("bytes_to_client", received),
		zap.Duration("duration", time.Since(start)))
}

// relayTunnel splices a WebSocket and a TCP connection until either side ends,
// a cap is reached, or the tunnel goes idle. It returns the bytes carried in
// each direction and closes both connections before returning.
func (g *Gateway) relayTunnel(conn *websocket.Conn, upstream net.Conn) (toDest, toClient int64) {
	// Lifetime ceiling. Both relay directions select on this, so neither can
	// outlive it even if its peer is silent.
	deadline := time.Now().Add(tunnelMaxDuration)
	_ = upstream.SetDeadline(deadline)

	var once sync.Once
	shutdown := func() {
		once.Do(func() {
			_ = upstream.Close()
			_ = conn.Close()
		})
	}
	defer shutdown()

	// The read deadline is the idle timeout, refreshed on every frame. A tunnel
	// that carries nothing in either direction for tunnelIdleTimeout is closed.
	conn.SetReadLimit(tunnelReadBuffer * 4)
	_ = conn.SetReadDeadline(time.Now().Add(tunnelIdleTimeout))

	var wg sync.WaitGroup
	wg.Add(2)

	// client → destination
	go func() {
		defer wg.Done()
		defer shutdown()
		for {
			msgType, data, err := conn.ReadMessage()
			if err != nil {
				return
			}
			if msgType != websocket.BinaryMessage {
				// The tunnel carries opaque bytes. A text frame means the peer
				// is speaking some other protocol; relaying it would corrupt
				// the stream silently.
				return
			}
			if toDest+int64(len(data)) > tunnelMaxBytes {
				g.logger.ComponentWarn(logging.ComponentGeneral, "tunnel closed: upload cap reached",
					zap.Int64("bytes", toDest))
				return
			}
			_ = upstream.SetWriteDeadline(time.Now().Add(tunnelWriteTimeout))
			n, werr := upstream.Write(data)
			toDest += int64(n)
			if werr != nil {
				return
			}
			_ = conn.SetReadDeadline(time.Now().Add(tunnelIdleTimeout))
		}
	}()

	// destination → client
	go func() {
		defer wg.Done()
		defer shutdown()
		buf := make([]byte, tunnelReadBuffer)
		for {
			_ = upstream.SetReadDeadline(minTime(time.Now().Add(tunnelIdleTimeout), deadline))
			n, rerr := upstream.Read(buf)
			if n > 0 {
				if toClient+int64(n) > tunnelMaxBytes {
					g.logger.ComponentWarn(logging.ComponentGeneral, "tunnel closed: download cap reached",
						zap.Int64("bytes", toClient))
					return
				}
				_ = conn.SetWriteDeadline(time.Now().Add(tunnelWriteTimeout))
				if werr := conn.WriteMessage(websocket.BinaryMessage, buf[:n]); werr != nil {
					return
				}
				toClient += int64(n)
				_ = conn.SetReadDeadline(time.Now().Add(tunnelIdleTimeout))
			}
			if rerr != nil {
				if rerr != io.EOF {
					return
				}
				return
			}
		}
	}()

	wg.Wait()
	return toDest, toClient
}

func minTime(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}
