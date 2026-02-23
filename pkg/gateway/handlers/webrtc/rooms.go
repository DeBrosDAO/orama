package webrtc

import (
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/DeBrosOfficial/network/pkg/logging"
	"go.uber.org/zap"
)

// RoomsHandler handles GET /v1/webrtc/rooms (list rooms)
// and GET /v1/webrtc/rooms?room_id=X (get specific room)
// Proxies to the local SFU's health endpoint for room data.
func (h *WebRTCHandlers) RoomsHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	ns := resolveNamespaceFromRequest(r)
	if ns == "" {
		writeError(w, http.StatusForbidden, "namespace not resolved")
		return
	}

	if h.sfuPort <= 0 {
		writeError(w, http.StatusServiceUnavailable, "SFU not configured")
		return
	}

	// Proxy to SFU health endpoint which returns room count
	targetURL := fmt.Sprintf("http://%s:%d/health", h.sfuHost, h.sfuPort)

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(targetURL)
	if err != nil {
		h.logger.ComponentWarn(logging.ComponentGeneral, "SFU health check failed",
			zap.String("namespace", ns),
			zap.Error(err),
		)
		writeError(w, http.StatusServiceUnavailable, "SFU unavailable")
		return
	}
	defer resp.Body.Close()

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(resp.StatusCode)
	io.Copy(w, resp.Body)
}
