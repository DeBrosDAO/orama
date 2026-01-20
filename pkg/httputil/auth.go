package httputil

import (
	"net/http"
	"strings"
)

// ExtractBearerToken extracts a Bearer token from the Authorization header.
// Returns an empty string if no Bearer token is found.
func ExtractBearerToken(r *http.Request) string {
	auth := r.Header.Get("Authorization")
	if auth == "" {
		return ""
	}

	lower := strings.ToLower(auth)
	if strings.HasPrefix(lower, "bearer ") {
		return strings.TrimSpace(auth[len("Bearer "):])
	}

	return ""
}

// ExtractAPIKey extracts an API key from various sources:
// 1. X-API-Key header (highest priority)
// 2. Authorization header with "ApiKey" scheme
// 3. Authorization header with "Bearer" scheme (if not a JWT)
// 4. Query parameter "api_key"
// 5. Query parameter "token"
//
// Note: Bearer tokens that look like JWTs (have 2 dots) are skipped.
func ExtractAPIKey(r *http.Request) string {
	// Prefer X-API-Key header (most explicit)
	if v := strings.TrimSpace(r.Header.Get("X-API-Key")); v != "" {
		return v
	}

	// Check Authorization header for ApiKey scheme or non-JWT Bearer tokens
	auth := r.Header.Get("Authorization")
	if auth != "" {
		lower := strings.ToLower(auth)
		if strings.HasPrefix(lower, "bearer ") {
			tok := strings.TrimSpace(auth[len("Bearer "):])
			// Skip Bearer tokens that look like JWTs (have 2 dots)
			if strings.Count(tok, ".") != 2 {
				return tok
			}
		} else if strings.HasPrefix(lower, "apikey ") {
			return strings.TrimSpace(auth[len("ApiKey "):])
		} else if !strings.Contains(auth, " ") {
			// If header has no scheme, treat the whole value as token
			tok := strings.TrimSpace(auth)
			if strings.Count(tok, ".") != 2 {
				return tok
			}
		}
	}

	// Fallback to query parameter (for WebSocket support)
	if v := strings.TrimSpace(r.URL.Query().Get("api_key")); v != "" {
		return v
	}

	// Also check token query parameter (alternative name)
	if v := strings.TrimSpace(r.URL.Query().Get("token")); v != "" {
		return v
	}

	return ""
}

// ExtractBasicAuth extracts username and password from Basic authentication.
// Returns empty strings if Basic auth is not present or invalid.
func ExtractBasicAuth(r *http.Request) (username, password string, ok bool) {
	return r.BasicAuth()
}

// HasAuthHeader checks if the request has any Authorization header.
func HasAuthHeader(r *http.Request) bool {
	return r.Header.Get("Authorization") != ""
}

// IsJWT checks if a token looks like a JWT (has exactly 2 dots separating 3 parts).
func IsJWT(token string) bool {
	return strings.Count(token, ".") == 2
}

// ExtractNamespaceHeader extracts the namespace from the X-Namespace header.
func ExtractNamespaceHeader(r *http.Request) string {
	return strings.TrimSpace(r.Header.Get("X-Namespace"))
}

// ExtractWalletHeader extracts the wallet address from the X-Wallet header.
func ExtractWalletHeader(r *http.Request) string {
	return strings.TrimSpace(r.Header.Get("X-Wallet"))
}
