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

	"github.com/DeBrosOfficial/network/pkg/gateway/auth"
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
// The MAC comes in two versions. v1 covers the fields above. v2 covers them and
// the verified token's exp, iat and jti, which a namespace gateway needs to hold
// an open WebSocket to the token that opened it: without them a socket proxied
// from the main gateway had no expiry, and no revocation could reach it.
//
// A signer stamps both, so a namespace gateway that predates v2 still accepts
// what an upgraded main gateway sends it. A verifier checks v2 whenever it is
// present, and v1 only when it is not — a hop from a main gateway that predates
// v2, which exists only during a rolling upgrade. Such a hop has the fields v1
// does not cover deleted, and hopTokenTimes gives its claims the most time a
// token could have left, so a socket opened through it still ends.
//
// Accepting v1 gives nobody anything they did not have. Stripping the v2 MAC
// off a genuine hop takes a position inside the WireGuard mesh, which is a
// node, and every node holds the cluster secret the MAC is keyed from — so
// whoever could downgrade a hop could sign one outright.
const (
	// HeaderInternalAuthMAC authenticates the v1 fields. Its value is
	// "<unix seconds>.<hex hmac>".
	HeaderInternalAuthMAC = "X-Internal-Auth-MAC"

	// HeaderInternalAuthMACv2 authenticates every X-Internal-Auth-* field, in
	// the same format.
	HeaderInternalAuthMACv2 = "X-Internal-Auth-MAC-V2"

	// HeaderInternalAuthJWTExp and HeaderInternalAuthJWTIat carry the verified
	// token's exp and iat (unix seconds), HeaderInternalAuthJWTJti its jti.
	// Covered by the v2 MAC only.
	HeaderInternalAuthJWTExp = "X-Internal-Auth-JWT-Exp"
	HeaderInternalAuthJWTIat = "X-Internal-Auth-JWT-Iat"
	HeaderInternalAuthJWTJti = "X-Internal-Auth-JWT-Jti"

	// internalAuthKeyPurpose is the HKDF domain separator for the hop key, so
	// it is unrelated to the TURN, push and secrets keys derived from the same
	// cluster secret.
	internalAuthKeyPurpose = "internal-auth-hop"

	// internalAuthMaxSkew bounds replay. A proxy hop is a single request over
	// the WireGuard mesh, so a minute is generous; it exists to tolerate clock
	// drift between nodes, not network delay.
	internalAuthMaxSkew = 60 * time.Second

	// The version prefixes of the two payloads, so one cannot be confused with
	// the other.
	internalAuthPayloadV1 = "orama-internal-auth-v1"
	internalAuthPayloadV2 = "orama-internal-auth-v2"
)

// internalAuthV2OnlyHeaders are the fields only the v2 MAC covers, in the
// order the v2 payload lists them. A hop carrying only a v1 MAC has them
// deleted before anything reads them.
var internalAuthV2OnlyHeaders = []string{
	HeaderInternalAuthJWTExp,
	HeaderInternalAuthJWTIat,
	HeaderInternalAuthJWTJti,
}

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
// be edited in flight.
func internalAuthPayload(version, method, path string, h http.Header, ts int64) string {
	fields := []string{
		version,
		strings.ToUpper(method),
		path,
		h.Get(HeaderInternalAuthNamespace),
		h.Get(HeaderInternalAuthJWTSub),
		h.Get(HeaderInternalAuthJWTCustom),
		h.Get(HeaderInternalAuthScopes),
	}
	if version == internalAuthPayloadV2 {
		for _, name := range internalAuthV2OnlyHeaders {
			fields = append(fields, h.Get(name))
		}
	}
	return strings.Join(append(fields, strconv.FormatInt(ts, 10)), "\n")
}

func internalAuthMAC(key []byte, version, method, path string, h http.Header, ts int64) []byte {
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(internalAuthPayload(version, method, path, h, ts)))
	return mac.Sum(nil)
}

// signInternalAuthHeaders stamps both MACs over the internal-auth headers
// already set on h. Call it last, after every X-Internal-Auth-* value is final.
func signInternalAuthHeaders(key []byte, h http.Header, method, path string, now time.Time) error {
	if len(key) == 0 {
		return fmt.Errorf("no internal-auth key: this gateway has no cluster secret, " +
			"so it cannot prove to a namespace gateway that it validated the request")
	}

	ts := now.Unix()
	stamp := strconv.FormatInt(ts, 10) + "."
	h.Set(HeaderInternalAuthMAC,
		stamp+hex.EncodeToString(internalAuthMAC(key, internalAuthPayloadV1, method, path, h, ts)))
	h.Set(HeaderInternalAuthMACv2,
		stamp+hex.EncodeToString(internalAuthMAC(key, internalAuthPayloadV2, method, path, h, ts)))
	return nil
}

// verifyInternalAuthHeaders reports whether the internal-auth headers on this
// request were stamped by a gateway holding the cluster secret.
//
// A request carrying a v2 MAC is judged by it alone, with no second chance
// under v1. It answers false for every reason: no key configured on this side,
// no MAC, a malformed MAC, a stale or future timestamp, or a MAC over different
// values than the headers now carry.
func verifyInternalAuthHeaders(key []byte, r *http.Request, now time.Time) bool {
	if len(key) == 0 {
		return false
	}
	version, header := internalAuthPayloadV2, HeaderInternalAuthMACv2
	if !hasInternalAuthMACv2(r.Header) {
		version, header = internalAuthPayloadV1, HeaderInternalAuthMAC
	}

	raw := strings.TrimSpace(r.Header.Get(header))
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
	return hmac.Equal(presented, internalAuthMAC(key, version, r.Method, r.URL.Path, r.Header, ts))
}

// hopTokenTimes reads the forwarded token's exp and iat.
//
// A hop that carries no exp — one from a main gateway that predates v2, whose
// v2-only headers were deleted as unauthenticated — cannot say when its token
// expires. Its claims are given the most the token can have left,
// MaxTokenLifetime from now, rather than no expiry, so a socket opened through
// it still ends. Its iat is put one revocation-list staleness back: the main
// gateway applied the list when it verified the token, so only a revocation
// newer than that could have been missed, and putting it further back would
// have an older "log out everywhere" close the fresh session that followed it.
// An exp of zero or less is treated the same way: no token this cluster mints
// has one.
func hopTokenTimes(h http.Header, now time.Time) (exp, iat int64, err error) {
	raw := strings.TrimSpace(h.Get(HeaderInternalAuthJWTExp))
	if raw == "" {
		return now.Add(auth.MaxTokenLifetime).Unix(), now.Add(-auth.RevocationRefreshInterval).Unix(), nil
	}
	exp, err = strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return 0, 0, fmt.Errorf("the forwarded token expiry %q is not a unix time: %w", raw, err)
	}
	iatRaw := strings.TrimSpace(h.Get(HeaderInternalAuthJWTIat))
	iat, err = strconv.ParseInt(iatRaw, 10, 64)
	if err != nil {
		return 0, 0, fmt.Errorf("the forwarded token issue time %q is not a unix time: %w", iatRaw, err)
	}
	if exp <= 0 {
		exp = now.Add(auth.MaxTokenLifetime).Unix()
	}
	return exp, iat, nil
}

func hasInternalAuthMACv2(h http.Header) bool {
	return strings.TrimSpace(h.Get(HeaderInternalAuthMACv2)) != ""
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
		switch {
		case !verifyInternalAuthHeaders(g.internalAuthKey, r, time.Now()):
			if r.Header.Get(HeaderInternalAuthValidated) != "" {
				g.logger.ComponentWarn("gateway", "dropped unauthenticated internal-auth headers",
					zap.String("path", r.URL.Path),
					zap.String("remote", remoteAddrIP(r)))
			}
			stripInboundInternalAuthHeaders(r.Header)
		case !hasInternalAuthMACv2(r.Header):
			// A v1 hop authenticated only the v1 fields; anything else it
			// carries is not read.
			for _, name := range internalAuthV2OnlyHeaders {
				r.Header.Del(name)
			}
		}
		// The MACs have served their purpose and must not travel further: a
		// deployed app or an outbound proxy target has no business seeing them.
		r.Header.Del(HeaderInternalAuthMAC)
		r.Header.Del(HeaderInternalAuthMACv2)
		next.ServeHTTP(w, r)
	})
}
