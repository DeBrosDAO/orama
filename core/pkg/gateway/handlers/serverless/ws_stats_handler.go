package serverless

import (
	"encoding/json"
	"net/http"
	"strings"
)

// WSConnections handles GET /v1/serverless/ws/connections
// Returns per-connection metrics for all active WS clients on this gateway.
//
// Optional path: /v1/serverless/ws/connections/{client_id} returns a single
// connection's snapshot (404 if not present).
//
// Auth: relies on the existing namespace-ownership middleware. Operators
// inspect their own gateway's connections; per-namespace filtering is not
// applied here because client IDs are gateway-local UUIDs unrelated to
// namespace.
func (h *ServerlessHandlers) WSConnections(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if h.wsManager == nil {
		http.Error(w, "ws manager not initialized", http.StatusServiceUnavailable)
		return
	}

	// Optional trailing path segment = client ID.
	const prefix = "/v1/serverless/ws/connections/"
	if strings.HasPrefix(r.URL.Path, prefix) {
		id := strings.TrimSuffix(r.URL.Path[len(prefix):], "/")
		if id == "" {
			h.respondJSON(w, http.StatusOK,
				map[string]interface{}{"connections": h.wsManager.ListConnStats()})
			return
		}
		stats, ok := h.wsManager.GetConnStats(id)
		if !ok {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		h.respondJSON(w, http.StatusOK, stats)
		return
	}

	h.respondJSON(w, http.StatusOK,
		map[string]interface{}{"connections": h.wsManager.ListConnStats()})
}

func (h *ServerlessHandlers) respondJSON(w http.ResponseWriter, status int, body interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
