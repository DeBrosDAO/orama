package gateway

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/DeBrosOfficial/network/pkg/secrets"
	"go.uber.org/zap"
)

// The main gateway validates a request and forwards the result to a namespace
// gateway in the X-Internal-Auth-* headers: the namespace it resolved, the JWT
// subject it verified, and the grant set of the API key it looked up. The
// namespace gateway believes all three without re-checking anything.
//
// Whether to believe them was answered by the source IP, and the source IP of
// every public request is 127.0.0.1. Caddy terminates TLS and reverse-proxies
// to localhost, and it forwards client headers verbatim, so anyone on the
// internet could send
//
//	X-Internal-Auth-Validated: true
//	X-Internal-Auth-Namespace: <any namespace>
//	X-Internal-Auth-Scopes: admin
//
// and be an admin of any namespace on any gateway, holding no credential at
// all. The ownership gate skipped its checks for the same reason.
//
// The headers now carry a MAC over exactly what they assert, keyed by a value
// derived from the cluster secret, and the source IP is not consulted at all.
// A caller who does not hold the cluster secret cannot produce one, and a MAC
// captured from a harmless request cannot be replayed onto a different method,
// path, namespace, subject or grant set.
//
// This is the Phase 0 shape. Phase 2 replaces the forwarded headers with a
// short-lived signed token carrying the same claims.
const (
	// HeaderInternalAuthMAC authenticates the other X-Internal-Auth-* headers.
	// Its value is "<unix seconds>.<hex hmac>".
	HeaderInternalAuthMAC = "X-Internal-Auth-MAC"

	// internalAuthKeyPurpose is the HKDF domain separator for the hop key, so
	// it is unrelated to the TURN, push and secrets keys derived from the same
	// cluster secret.
	internalAuthKeyPurpose = "internal-auth-hop"

	// internalAuthMaxSkew bounds replay. A proxy hop is a single request over
	// the WireGuard mesh, so a minute is generous; it exists to tolerate clock
	// drift between nodes, not network delay.
	internalAuthMaxSkew = 60 * time.Second
)

// internalAuthKey derives the key both ends of a proxy hop use.
//
// Every node in a cluster holds the same cluster secret, which is what makes
// this work across the mesh — and what makes it useless to anyone outside it.
func internalAuthKey(clusterSecret string) ([]byte, error) {
	return secrets.DeriveKey(clusterSecret, internalAuthKeyPurpose)
}

// internalAuthPayload is the exact string a MAC covers.
//
// Method and path are in it so a MAC observed on a GET cannot be replayed onto
// a DELETE, and every field the receiving gateway trusts is in it so none can
// be edited in flight. The version prefix means a later format change cannot be
// confused with this one.
func internalAuthPayload(method, path, namespace, sub, custom, scopes string, ts int64) string {
	return strings.Join([]string{
		"orama-internal-auth-v1",
		strings.ToUpper(method),
		path,
		namespace,
		sub,
		custom,
		scopes,
		strconv.FormatInt(ts, 10),
	}, "\n")
}

// signInternalAuthHeaders stamps a MAC over the internal-auth headers already
// set on h. Call it last, after every X-Internal-Auth-* value is final.
func signInternalAuthHeaders(key []byte, h http.Header, method, path string, now time.Time) error {
	if len(key) == 0 {
		return fmt.Errorf("no internal-auth key: this gateway has no cluster secret, " +
			"so it cannot prove to a namespace gateway that it validated the request")
	}

	ts := now.Unix()
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(internalAuthPayload(
		method, path,
		h.Get(HeaderInternalAuthNamespace),
		h.Get(HeaderInternalAuthJWTSub),
		h.Get(HeaderInternalAuthJWTCustom),
		h.Get(HeaderInternalAuthScopes),
		ts,
	)))

	h.Set(HeaderInternalAuthMAC, strconv.FormatInt(ts, 10)+"."+hex.EncodeToString(mac.Sum(nil)))
	return nil
}

// verifyInternalAuthHeaders reports whether the internal-auth headers on this
// request were stamped by a gateway holding the cluster secret.
//
// It answers false for every reason: no key configured on this side, no MAC,
// a malformed MAC, a stale or future timestamp, or a MAC over different values
// than the headers now carry.
func verifyInternalAuthHeaders(key []byte, r *http.Request, now time.Time) bool {
	if len(key) == 0 {
		return false
	}

	raw := strings.TrimSpace(r.Header.Get(HeaderInternalAuthMAC))
	stamp, sig, ok := strings.Cut(raw, ".")
	if !ok {
		return false
	}
	ts, err := strconv.ParseInt(stamp, 10, 64)
	if err != nil {
		return false
	}
	// Both directions: a future timestamp is as much a sign of a forged or
	// replayed stamp as an old one.
	if skew := now.Sub(time.Unix(ts, 0)); skew > internalAuthMaxSkew || skew < -internalAuthMaxSkew {
		return false
	}
	presented, err := hex.DecodeString(sig)
	if err != nil {
		return false
	}

	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(internalAuthPayload(
		r.Method, r.URL.Path,
		r.Header.Get(HeaderInternalAuthNamespace),
		r.Header.Get(HeaderInternalAuthJWTSub),
		r.Header.Get(HeaderInternalAuthJWTCustom),
		r.Header.Get(HeaderInternalAuthScopes),
		ts,
	)))
	return hmac.Equal(presented, mac.Sum(nil))
}

// internalAuthMiddleware is the first thing every request meets.
//
// Headers that arrived without a valid MAC are deleted before any other
// middleware can read them, so nothing downstream has to remember to ask
// whether they were authentic. A request that forges them is not rejected —
// it is simply treated as what it is, a request with no internal-auth headers,
// and goes on to authenticate normally or be refused for having no credential.
func (g *Gateway) internalAuthMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !verifyInternalAuthHeaders(g.internalAuthKey, r, time.Now()) {
			if r.Header.Get(HeaderInternalAuthValidated) != "" {
				g.logger.ComponentWarn("gateway", "dropped unauthenticated internal-auth headers",
					zap.String("path", r.URL.Path),
					zap.String("remote", remoteAddrIP(r)))
			}
			stripInboundInternalAuthHeaders(r.Header)
		}
		// The MAC has served its purpose and must not travel further: a
		// deployed app or an outbound proxy target has no business seeing it.
		r.Header.Del(HeaderInternalAuthMAC)
		next.ServeHTTP(w, r)
	})
}
