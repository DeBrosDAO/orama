package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const (
	// CoordinationMACV3Header carries "<unix seconds>.<hex hmac>" over the v3
	// payload. A request is stamped with it beside the v2 and v1 stamps, so a
	// node still on a build that reads only those keeps accepting it during a
	// rolling upgrade. It shares CoordinationNonceHeader with the v2 stamp: a
	// request is one request, and a verifier consumes its nonce once.
	CoordinationMACV3Header = "X-Orama-Coordination-MAC-V3"

	// AcceptLegacyCoordinationV2 lets a request stamped with v2 and no v3
	// verify, for callers not upgraded yet (a node on the previous build, and a
	// Caddy whose TLS-store module is older than the gateway). A v2 stamp names
	// a node, not a process, so while this is set a stamp for a route several
	// processes on one node serve can be replayed once to a sibling process
	// inside the window. It is removed in the release after the one that
	// introduced v3 (docs/SECURITY.md, "Coordination MAC v3").
	AcceptLegacyCoordinationV2 = true
)

// coordinationPayloadV3 is the v2 payload plus scope, the port of the process
// the request is for. Several processes on one node (the index gateway and
// every namespace gateway) share the node's peer id but each has its own nonce
// cache and its own port, so the port is what makes a stamp good for one of
// them. The label differs from v2's, so a v2 MAC is never a valid v3 MAC.
func coordinationPayloadV3(method, audience, scope, path, query string, body []byte, nonce string, ts int64) string {
	sum := sha256.Sum256(body)
	return strings.Join([]string{
		"orama-coordination-v3",
		strings.ToUpper(method),
		audience,
		scope,
		path,
		query,
		hex.EncodeToString(sum[:]),
		nonce,
		strconv.FormatInt(ts, 10),
	}, "\n")
}

// setCoordinationStamp sets header to "<ts>.<hex hmac of payload>".
func setCoordinationStamp(key []byte, r *http.Request, header string, ts int64, payload string) {
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(payload))
	r.Header.Set(header, strconv.FormatInt(ts, 10)+"."+hex.EncodeToString(mac.Sum(nil)))
}

// requestPort is the port a signer is sending r to: the URL's, or the scheme's
// default when it names none, and "" for a URL with no host (a request built
// but not yet addressed).
func requestPort(r *http.Request) string {
	if port := r.URL.Port(); port != "" {
		return port
	}
	switch r.URL.Scheme {
	case "http":
		return "80"
	case "https":
		return "443"
	}
	return ""
}

// coordinationServedPort is the port the connection r arrived on, as the
// server saw it. It is
// read from the connection and never from the request, so a replayer cannot
// pick it; the Host header, which it can, is not consulted. A request that did
// not come through a server (one built in a test) has no connection, and is
// scoped by the URL it was built with, which a real server's request never
// carries in that form.
func coordinationServedPort(r *http.Request) string {
	addr, ok := r.Context().Value(http.LocalAddrContextKey).(net.Addr)
	if !ok {
		return requestPort(r)
	}
	_, port, err := net.SplitHostPort(addr.String())
	if err != nil {
		return ""
	}
	return port
}

func verifyCoordinationV3(key []byte, r *http.Request, now time.Time, audience string) bool {
	scope := coordinationServedPort(r)
	return verifyCoordinationNonced(key, r, now, CoordinationMACV3Header,
		func(body []byte, nonce string, ts int64) string {
			return coordinationPayloadV3(r.Method, audience, scope, r.URL.Path, r.URL.RawQuery, body, nonce, ts)
		})
}
