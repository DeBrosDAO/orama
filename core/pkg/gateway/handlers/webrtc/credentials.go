package webrtc

import (
	"fmt"
	"net/http"
	"time"

	"github.com/DeBrosOfficial/network/pkg/logging"
	"github.com/DeBrosOfficial/network/pkg/turn"
	"go.uber.org/zap"
)

const turnCredentialTTL = 10 * time.Minute

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

	// Build TURN URIs — use IPs to bypass DNS propagation delays
	var uris []string
	if h.turnDomain != "" {
		uris = append(uris,
			fmt.Sprintf("turn:%s:3478?transport=udp", h.turnDomain),
			fmt.Sprintf("turn:%s:3478?transport=tcp", h.turnDomain),
			fmt.Sprintf("turns:%s:5349", h.turnDomain),
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
