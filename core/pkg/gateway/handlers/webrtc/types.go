package webrtc

import (
	"encoding/json"
	"net/http"

	"github.com/DeBrosOfficial/network/pkg/gateway/ctxkeys"
	"github.com/DeBrosOfficial/network/pkg/logging"
)

// WebRTCHandlers handles all WebRTC-related HTTP and WebSocket endpoints.
// These run on the namespace gateway and proxy signaling to the local SFU.
type WebRTCHandlers struct {
	logger     *logging.ColoredLogger
	sfuHost    string // SFU host IP (WireGuard IP) to proxy connections to
	sfuPort    int    // Local SFU signaling port to proxy WebSocket connections to
	turnDomain string // TURN server domain for plain UDP/TCP URIs (turn:…:3478)
	turnSecret string // HMAC-SHA1 shared secret for TURN credential generation

	// turnsTLSDomain is the single-label host (turn-<ns>.<base>) used for the
	// turns:…:5349 TLS URI. It is covered by the *.<base> wildcard cert, so
	// TURNS validates in browsers — unlike turnDomain (two labels), which can't
	// get a CA-valid cert. Empty → fall back to turnDomain (pre-fix behavior).
	turnsTLSDomain string

	// stealthCDNDomain, when non-empty, causes CredentialsHandler to also
	// advertise turns://<stealthCDNDomain>:443 — the stealth TURN URI served
	// via the in-house SNI router. See pkg/sniproxy.
	stealthCDNDomain string

	// proxyWebSocket is injected from the gateway to reuse its WebSocket proxy logic
	proxyWebSocket func(w http.ResponseWriter, r *http.Request, targetHost string) bool
}

// SetStealthCDNDomain enables the stealth TURN URI in CredentialsHandler.
// Pass empty string to disable. Safe to call before serving begins.
func (h *WebRTCHandlers) SetStealthCDNDomain(domain string) {
	h.stealthCDNDomain = domain
}

// SetTURNSTLSDomain sets the single-label host used for the turns:…:5349 TLS
// URI. Pass empty to fall back to the plain turnDomain. Safe to call before
// serving begins.
func (h *WebRTCHandlers) SetTURNSTLSDomain(domain string) {
	h.turnsTLSDomain = domain
}

// NewWebRTCHandlers creates a new WebRTCHandlers instance.
func NewWebRTCHandlers(
	logger *logging.ColoredLogger,
	sfuHost string,
	sfuPort int,
	turnDomain string,
	turnSecret string,
	proxyWS func(w http.ResponseWriter, r *http.Request, targetHost string) bool,
) *WebRTCHandlers {
	if sfuHost == "" {
		sfuHost = "127.0.0.1"
	}
	return &WebRTCHandlers{
		logger:         logger,
		sfuHost:        sfuHost,
		sfuPort:        sfuPort,
		turnDomain:     turnDomain,
		turnSecret:     turnSecret,
		proxyWebSocket: proxyWS,
	}
}

// resolveNamespaceFromRequest gets namespace from context set by auth middleware
func resolveNamespaceFromRequest(r *http.Request) string {
	if v := r.Context().Value(ctxkeys.NamespaceOverride); v != nil {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}
