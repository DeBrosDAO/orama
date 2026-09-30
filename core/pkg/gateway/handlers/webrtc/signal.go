package webrtc

import (
	"errors"
	"net/http"

	"github.com/DeBrosOfficial/network/pkg/logging"
	"go.uber.org/zap"
)

// roomQueryParam carries the room the socket will join, so the gateway can
// route the upgrade before the join frame exists. The SFU rejects a join whose
// roomId differs from it.
const roomQueryParam = "room"

// SignalHandler handles WebSocket /v1/webrtc/signal.
// It proxies the socket to the SFU that owns the requested room, wherever in
// the namespace that SFU runs, so every peer of a room meets in one process.
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

	room := r.URL.Query().Get(roomQueryParam)
	if room == "" {
		writeError(w, http.StatusBadRequest, "the room query parameter is required: /v1/webrtc/signal?room=<roomId>")
		return
	}

	if h.proxyWebSocket == nil {
		writeError(w, http.StatusInternalServerError, "WebSocket proxy not available")
		return
	}

	owner, err := h.ownerOf(r.Context(), ns, room)
	if err != nil {
		h.logger.ComponentWarn(logging.ComponentGeneral, "No SFU available for room",
			zap.String("namespace", ns), zap.String("room", room), zap.Error(err))
		writeError(w, http.StatusServiceUnavailable, ownerErrorMessage(err))
		return
	}
	targetHost := owner.Addr()

	h.logger.ComponentDebug(logging.ComponentGeneral, "Proxying WebRTC signal to room owner",
		zap.String("namespace", ns),
		zap.String("room", room),
		zap.String("sfu_node", owner.NodeID),
		zap.String("target", targetHost),
	)

	// Rewrite the URL path to match the SFU's expected endpoint
	r.URL.Path = "/ws/signal"
	r.URL.Scheme = "http"
	r.URL.Host = targetHost
	r.Host = targetHost

	if !h.proxyWebSocket(w, r, targetHost) {
		// proxyWebSocket already wrote the error response
		h.logger.ComponentWarn(logging.ComponentGeneral, "SFU WebSocket proxy failed",
			zap.String("namespace", ns),
			zap.String("target", targetHost),
		)
	}
}

// ownerErrorMessage is the client-facing reason a room could not be placed.
func ownerErrorMessage(err error) string {
	switch {
	case errors.Is(err, ErrNoSFUNodes):
		return "WebRTC has no SFU nodes for this namespace; enable it with `orama namespace enable webrtc`"
	case errors.Is(err, ErrNoHealthySFU):
		return "no SFU node is ready; retry shortly, and check `orama namespace webrtc-status`"
	default:
		return "cannot place the room on an SFU: the SFU registry is unavailable"
	}
}
