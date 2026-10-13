package gateway

import "net/http"

const (
	// webrtcJoinsPerMinute and webrtcJoinBurst bound the signalling sockets one
	// identity may open. A join costs the gateway a registry read and a health
	// probe per SFU node, so a client looping on reconnect must not be able to
	// turn that into load on the SFUs. A call is one socket; a burst covers a
	// reconnect after an SFU drains.
	webrtcJoinsPerMinute = 60
	webrtcJoinBurst      = 20
)

// webrtcJoinAllowed limits joins per signed-in identity. The namespace gateway
// sees only the overlay address of the gateway that proxied the client, which is
// exempt from address limits, so the wallet subject is the identity; an address
// is used only when no subject is present and the caller is not internal.
func (g *Gateway) webrtcJoinAllowed(r *http.Request) bool {
	if g.webrtcJoinRateLimiter == nil {
		return true
	}
	if sub := tunnelCallerIdentity(r); sub != "" {
		return g.webrtcJoinRateLimiter.Allow("sub:" + sub)
	}
	client, exempt := rateLimitClient(r)
	if exempt {
		return true // internal traffic carries no client to hold responsible
	}
	return g.webrtcJoinRateLimiter.Allow("addr:" + bucketKey(client))
}
