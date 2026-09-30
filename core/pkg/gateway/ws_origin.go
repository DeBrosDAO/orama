package gateway

import (
	"net/http"

	"github.com/DeBrosOfficial/network/pkg/httputil"
)

// refuseCrossSiteUpgrade answers 403 to a WebSocket upgrade a browser on
// another site is making, and reports whether it did.
//
// The upgrader on the namespace gateway checks Origin too, but it is the last
// thing a request reaches: after the credential has been validated and a live
// gateway chosen. A cross-site upgrade for a namespace whose gateway is not
// resolvable was answered 404 ("Namespace gateway not found") rather than
// refused, so the refusal depended on the backend being up. The host compared
// against is the one this gateway was asked for. A client's own
// X-Forwarded-Host is discarded first: it is the value CheckWebSocketOrigin
// prefers, so leaving it would let the caller name the host its Origin is
// compared with.
func refuseCrossSiteUpgrade(w http.ResponseWriter, r *http.Request) bool {
	if !isWebSocketUpgrade(r) {
		return false
	}
	r.Header.Set("X-Forwarded-Host", r.Host)
	if httputil.CheckWebSocketOrigin(r) {
		return false
	}
	forbidden(w, CodeOriginNotAllowed, "websocket: request origin not allowed", nil)
	return true
}
