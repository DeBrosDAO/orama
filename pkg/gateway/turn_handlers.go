package gateway

import (
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base64"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/DeBrosOfficial/network/pkg/gateway/auth"
	"github.com/DeBrosOfficial/network/pkg/logging"
	"go.uber.org/zap"
)

// TURNCredentialsResponse is the response for TURN credential requests
type TURNCredentialsResponse struct {
	Username   string   `json:"username"`    // Format: "timestamp:userId"
	Credential string   `json:"credential"`  // HMAC-SHA1(username, shared_secret) base64 encoded
	TTL        int64    `json:"ttl"`         // Time-to-live in seconds
	STUNURLs   []string `json:"stun_urls"`   // STUN server URLs
	TURNURLs   []string `json:"turn_urls"`   // TURN server URLs
}

// turnCredentialsHandler handles POST /v1/turn/credentials
// Returns time-limited TURN credentials for WebRTC connections
func (g *Gateway) turnCredentialsHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost && r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	// Check if TURN is configured
	if g.cfg.TURN == nil || g.cfg.TURN.SharedSecret == "" {
		g.logger.ComponentWarn(logging.ComponentGeneral, "TURN credentials requested but not configured")
		writeError(w, http.StatusServiceUnavailable, "TURN service not configured")
		return
	}

	// Get user ID from JWT claims or API key
	userID := g.extractUserID(r)
	if userID == "" {
		userID = "anonymous"
	}

	// Get gateway hostname from request
	gatewayHost := r.Host
	if idx := strings.Index(gatewayHost, ":"); idx != -1 {
		gatewayHost = gatewayHost[:idx] // Remove port
	}
	if gatewayHost == "" {
		gatewayHost = "localhost"
	}

	// Generate credentials
	credentials := g.generateTURNCredentials(userID, gatewayHost)

	g.logger.ComponentInfo(logging.ComponentGeneral, "TURN credentials generated",
		zap.String("user_id", userID),
		zap.String("gateway_host", gatewayHost),
		zap.Int64("ttl", credentials.TTL),
	)

	writeJSON(w, http.StatusOK, credentials)
}

// generateTURNCredentials creates time-limited TURN credentials using HMAC-SHA1
func (g *Gateway) generateTURNCredentials(userID, gatewayHost string) *TURNCredentialsResponse {
	cfg := g.cfg.TURN

	// Default TTL to 24 hours if not configured
	ttl := cfg.TTL
	if ttl == 0 {
		ttl = 24 * time.Hour
	}

	// Calculate expiry timestamp
	timestamp := time.Now().Unix() + int64(ttl.Seconds())

	// Format: "timestamp:userId" (coturn format)
	username := fmt.Sprintf("%d:%s", timestamp, userID)

	// Generate HMAC-SHA1 credential
	h := hmac.New(sha1.New, []byte(cfg.SharedSecret))
	h.Write([]byte(username))
	credential := base64.StdEncoding.EncodeToString(h.Sum(nil))

	// Process URLs - replace empty hostnames with gateway host
	stunURLs := processURLsWithHost(cfg.STUNURLs, gatewayHost)
	turnURLs := processURLsWithHost(cfg.TURNURLs, gatewayHost)

	// If TLS is enabled, ensure we have turns:// URLs
	if cfg.TLSEnabled {
		hasTurns := false
		for _, url := range turnURLs {
			if strings.HasPrefix(url, "turns:") {
				hasTurns = true
				break
			}
		}
		// Auto-add turns:// URL if not already configured
		if !hasTurns {
			turnsURL := fmt.Sprintf("turns:%s:443?transport=tcp", gatewayHost)
			turnURLs = append(turnURLs, turnsURL)
		}
	}

	return &TURNCredentialsResponse{
		Username:   username,
		Credential: credential,
		TTL:        int64(ttl.Seconds()),
		STUNURLs:   stunURLs,
		TURNURLs:   turnURLs,
	}
}

// processURLsWithHost replaces empty hostnames in URLs with the given host
// e.g., "stun::3478" -> "stun:localhost:3478"
func processURLsWithHost(urls []string, host string) []string {
	result := make([]string, 0, len(urls))
	for _, url := range urls {
		// Check for empty hostname pattern like "stun::3478" or "turn::3478"
		if strings.Contains(url, "::") {
			// Replace :: with :host:
			url = strings.Replace(url, "::", ":"+host+":", 1)
		}
		result = append(result, url)
	}
	return result
}

// extractUserID extracts the user ID from the request context
func (g *Gateway) extractUserID(r *http.Request) string {
	ctx := r.Context()

	// Try JWT claims first
	if v := ctx.Value(ctxKeyJWT); v != nil {
		if claims, ok := v.(*auth.JWTClaims); ok && claims != nil {
			if claims.Sub != "" {
				return claims.Sub
			}
		}
	}

	// Fallback to API key
	if v := ctx.Value(ctxKeyAPIKey); v != nil {
		if key, ok := v.(string); ok && key != "" {
			// Use a hash of the API key as the user ID for privacy
			return fmt.Sprintf("ak_%s", key[:min(8, len(key))])
		}
	}

	return ""
}

