package auth

import (
	"net/http"
	"strings"
)

// APIKeyFromRequest reads the API key a request presents.
//
// There were two copies of this, and they drifted. The middleware's took a key
// from the query string only on a WebSocket upgrade, because a browser cannot
// set a header on one; the auth handler's took it from the query string on any
// request, so a POST to /v1/auth/token could carry a credential in its URL —
// into the access log, the Referer of anything the page loads next, and the
// browser's history. One copy, and the decision made by the caller.
//
// allowQueryString should be true only where a header genuinely cannot be sent.
func APIKeyFromRequest(r *http.Request, allowQueryString bool) string {
	// The most explicit form wins.
	if v := strings.TrimSpace(r.Header.Get("X-API-Key")); v != "" {
		return v
	}

	if header := r.Header.Get("Authorization"); header != "" {
		lower := strings.ToLower(header)
		switch {
		case strings.HasPrefix(lower, "bearer "):
			// A Bearer token with two dots is a JWT and is somebody else's
			// to verify.
			if tok := strings.TrimSpace(header[len("Bearer "):]); strings.Count(tok, ".") != 2 {
				return tok
			}
		case strings.HasPrefix(lower, "apikey "):
			return strings.TrimSpace(header[len("ApiKey "):])
		case !strings.Contains(header, " "):
			// No scheme at all: the whole value is the token, unless it is
			// a JWT.
			if tok := strings.TrimSpace(header); strings.Count(tok, ".") != 2 {
				return tok
			}
		}
	}

	if !allowQueryString {
		return ""
	}
	if v := strings.TrimSpace(r.URL.Query().Get("api_key")); v != "" {
		return v
	}
	if v := strings.TrimSpace(r.URL.Query().Get("token")); v != "" {
		return v
	}
	return ""
}
