package webrtc

import (
	"fmt"
	"net/http"

	"github.com/DeBrosOfficial/network/pkg/logging"
	"github.com/DeBrosOfficial/network/pkg/turn"
	"go.uber.org/zap"
)

// turnCredentialTTL is the lifetime of credentials issued by the HTTP path.
// Sourced from the shared pkg/turn default so this path and the WASM host-fn
// path (pkg/serverless/hostfunctions/turn.go) can never drift apart again
// (bugboard #155).
const turnCredentialTTL = turn.DefaultCredentialTTL

// CredentialsHandler handles POST /v1/webrtc/turn/credentials
// Returns fresh TURN credentials scoped to the authenticated namespace.
func (h *WebRTCHandlers) CredentialsHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	ns := resolveNamespaceFromRequest(r)
	if ns == "" {
		writeError(w, http.StatusForbidden, "namespace not resolved")
		return
	}

	if h.turnSecret == "" {
		writeError(w, http.StatusServiceUnavailable, "TURN not configured")
		return
	}

	username, password := turn.GenerateCredentials(h.turnSecret, ns, turnCredentialTTL)

	// Build TURN URIs. Plain UDP/TCP TURN (:3478) uses turnDomain (no cert
	// involved). TURNS (:5349) uses the single-label turnsTLSDomain, which the
	// *.<base> wildcard cert covers so browsers validate it — the two-label
	// turnDomain can only ever present a self-signed cert that browsers reject.
	// turnsTLSDomain empty → fall back to turnDomain (pre-fix behavior).
	var uris []string
	if h.turnDomain != "" {
		turnsHost := h.turnsTLSDomain
		if turnsHost == "" {
			turnsHost = h.turnDomain
		}
		uris = append(uris,
			fmt.Sprintf("turn:%s:3478?transport=udp", h.turnDomain),
			fmt.Sprintf("turn:%s:3478?transport=tcp", h.turnDomain),
			fmt.Sprintf("turns:%s:5349", turnsHost),
		)
	}
	// Stealth: TURNS via the SNI router on :443. Looks like ordinary HTTPS
	// to a passive observer / DPI; usable in restricted regions.
	if h.stealthCDNDomain != "" {
		uris = append(uris, fmt.Sprintf("turns:%s:443", h.stealthCDNDomain))
	}

	h.logger.ComponentInfo(logging.ComponentGeneral, "Issued TURN credentials",
		zap.String("namespace", ns),
		zap.String("username", username),
	)

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"username": username,
		"password": password,
		"ttl":      int(turnCredentialTTL.Seconds()),
		"uris":     uris,
	})
}
