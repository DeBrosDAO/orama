package webrtc

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/DeBrosOfficial/network/pkg/logging"
	"github.com/DeBrosOfficial/network/pkg/sfu/ctrlauth"
	"github.com/DeBrosOfficial/network/pkg/sfu/roomid"
	"go.uber.org/zap"
)

const (
	// roomQueryParam optionally carries the room the socket will join, so the
	// gateway can route the upgrade without reading the join frame. The SFU
	// rejects a join whose roomId differs from it.
	roomQueryParam = "room"

	// joinRetryAfterSeconds is the Retry-After of a rate-limited join.
	joinRetryAfterSeconds = 10
)

// SignalHandler handles WebSocket /v1/webrtc/signal.
// It sends the socket to the SFU that owns the requested room, wherever in the
// namespace that SFU runs, so every peer of a room meets in one process. The
// room comes from ?room= when present (the socket is then piped through as-is),
// otherwise from the client's join frame (signal_join.go).
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

	if h.joinAllowed != nil && !h.joinAllowed(r) {
		w.Header().Set("Retry-After", strconv.Itoa(joinRetryAfterSeconds))
		writeError(w, http.StatusTooManyRequests, "too many WebRTC joins from this identity; wait a moment and retry")
		return
	}

	room := r.URL.Query().Get(roomQueryParam)
	if room == "" {
		// A client that does not name the room in the URL is routed by its join frame.
		h.signalByJoinFrame(w, r, ns)
		return
	}
	if err := roomid.Validate(room); err != nil {
		writeError(w, http.StatusBadRequest, "invalid room query parameter: "+err.Error())
		return
	}

	if h.proxyWebSocket == nil {
		writeError(w, http.StatusInternalServerError, "WebSocket proxy not available")
		return
	}

	ticket, refusal := h.authorizeJoin(r.Context(), r, ns, room)
	if refusal != nil {
		refusal.write(w)
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

	// The ticket is what the SFU takes the peer's identity from. Whatever the
	// client sent under that name is replaced, never forwarded.
	r.Header.Set(ctrlauth.TicketHeader, ticket)

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
