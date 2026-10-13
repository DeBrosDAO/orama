package gateway

import (
	"net/http"
	"path"
	"strings"
)

const (
	// publicExplorerOrigin is where the Orama project serves its block explorer
	// (website/src/explorer). The explorer is a static page on that host and
	// reads a network's gateway (today stagenet's) from the reader's browser,
	// so its origin is not under that network's base domain (orama.network is
	// the parent of stagenet.orama.network, and getAllowedOrigin admits only
	// the base domain and the names under it).
	publicExplorerOrigin = "https://orama.network"

	// chainReadPathPrefix is the chain proxy (handlers/chainread). Everything a
	// GET reaches under it is public, unauthenticated chain data.
	chainReadPathPrefix = "/v1/chain/"
)

// allowedOriginFor is the Access-Control-Allow-Origin for r: the origin when the
// base-domain rule admits it (getAllowedOrigin), and also when it is the public
// explorer reading the chain. That exception covers only a read of /v1/chain/:
// the same origin gets no CORS on the credentialed routes, and none on the
// chain's POST routes (simulate, broadcast, light, faucet), which a page from
// another site has no business sending. No other origin is let in, and the
// answer carries no Allow-Credentials, so a browser attaches nothing of the
// reader's to the read.
func (g *Gateway) allowedOriginFor(r *http.Request) string {
	origin := r.Header.Get("Origin")
	if origin == publicExplorerOrigin && isChainRead(r) {
		return origin
	}
	return g.getAllowedOrigin(origin)
}

// isChainRead reports whether r is, or a preflight announces, a GET or HEAD of
// the chain proxy. A preflight is an OPTIONS for the path of the request it
// asks about, and names that request's method in Access-Control-Request-Method.
// A path the router would first rewrite or redirect (a ".." or "." segment, a
// doubled slash) is not a read of the proxy yet and is not let in.
func isChainRead(r *http.Request) bool {
	p := r.URL.Path
	if !strings.HasPrefix(p, chainReadPathPrefix) || path.Clean(p) != strings.TrimSuffix(p, "/") {
		return false
	}
	method := r.Method
	if method == http.MethodOptions {
		method = r.Header.Get("Access-Control-Request-Method")
	}
	return method == http.MethodGet || method == http.MethodHead
}
