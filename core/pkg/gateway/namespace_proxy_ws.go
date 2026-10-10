package gateway

import (
	"net/http"
	"strconv"

	"github.com/DeBrosOfficial/network/pkg/httputil"
	"github.com/DeBrosOfficial/network/pkg/logging"
	"go.uber.org/zap"
)

// proxyNamespaceWebSocket tunnels a WebSocket upgrade to a namespace gateway.
// Like an HTTP request (proxyToNamespaceGateway), an upgrade whose dial to the
// selected member fails was never sent anywhere, so the next member whose
// circuit allows it is tried instead of answering 503 until the failing
// member's circuit opens. For a room's signaling socket any member will do:
// each routes the room to the SFU that owns it (website/src/docs/operator/webrtc-operations.mdx#room-placement).
//
// targets is the ordered member list, selected the index of the member chosen
// with cb its circuit breaker. A tunnel that was established and then ended is
// not retried; it counts as the member's success, and a handshake that failed
// after the dial as its failure. When no member can be dialed the client gets a
// retryable NAMESPACE_GATEWAY_UNAVAILABLE.
func (g *Gateway) proxyNamespaceWebSocket(w http.ResponseWriter, r *http.Request,
	targets []namespaceGatewayTarget, selected int, cb *CircuitBreaker, namespaceName string) {
	for i := selected; i < len(targets); i++ {
		candidate := targets[i]
		candidateCB := cb
		if i != selected {
			if candidateCB = g.circuitBreakers.Get("ns:" + candidate.ip); !candidateCB.Allow() {
				continue
			}
		}
		targetHost := candidate.ip + ":" + strconv.Itoa(candidate.port)
		r.URL.Scheme = "http"
		r.URL.Host = targetHost
		r.Host = targetHost

		// Record the outcome. Without this a WS upgrade that happened to be the
		// half-open probe held the breaker's single probe slot for the life of
		// the process, silently removing a healthy node from the round-robin -
		// and a target that failed ONLY on WS never opened a breaker at all, so
		// it kept receiving signalling traffic forever.
		proxied, dialErr := g.tunnelWebSocket(w, r, targetHost)
		if dialErr == nil {
			if proxied {
				candidateCB.RecordSuccess()
			} else {
				candidateCB.RecordFailure()
			}
			return
		}
		candidateCB.RecordFailure()
		// A client that left says nothing about the member.
		if r.Context().Err() != nil {
			return
		}
		g.logger.ComponentWarn(logging.ComponentGeneral, "namespace gateway WebSocket dial failed, trying the next member",
			zap.String("namespace", namespaceName), zap.String("target", candidate.ip), zap.Error(dialErr))
	}
	httputil.WriteRPCError(w, http.StatusServiceUnavailable,
		httputil.ErrCodeNamespaceGatewayUnavailable,
		"no gateway of this namespace accepted the WebSocket connection; retry shortly, and check `orama monitor report` for unhealthy nodes",
		httputil.WithRetryable())
}
