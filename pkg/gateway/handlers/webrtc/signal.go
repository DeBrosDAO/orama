package webrtc

import (
	"fmt"
	"net/http"

	"github.com/DeBrosOfficial/network/pkg/logging"
	"go.uber.org/zap"
)

// SignalHandler handles WebSocket /v1/webrtc/signal
// Proxies the WebSocket connection to the local SFU's signaling endpoint.
func (h *WebRTCHandlers) SignalHandler(w http.ResponseWriter, r *http.Request) {
	ns := resolveNamespaceFromRequest(r)
	if ns == "" {
		writeError(w, http.StatusForbidden, "namespace not resolved")
		return
	}

	if h.sfuPort <= 0 {
		writeError(w, http.StatusServiceUnavailable, "SFU not configured")
		return
	}

	// Proxy WebSocket to local SFU on WireGuard IP
	// SFU binds to WireGuard IP, so we use 127.0.0.1 since we're on the same node
	targetHost := fmt.Sprintf("127.0.0.1:%d", h.sfuPort)

	h.logger.ComponentDebug(logging.ComponentGeneral, "Proxying WebRTC signal to SFU",
		zap.String("namespace", ns),
		zap.String("target", targetHost),
	)

	// Rewrite the URL path to match the SFU's expected endpoint
	r.URL.Path = "/ws/signal"
	r.URL.Scheme = "http"
	r.URL.Host = targetHost
	r.Host = targetHost

	if h.proxyWebSocket == nil {
		writeError(w, http.StatusInternalServerError, "WebSocket proxy not available")
		return
	}

	if !h.proxyWebSocket(w, r, targetHost) {
		// proxyWebSocket already wrote the error response
		h.logger.ComponentWarn(logging.ComponentGeneral, "SFU WebSocket proxy failed",
			zap.String("namespace", ns),
			zap.String("target", targetHost),
		)
	}
}
