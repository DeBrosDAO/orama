package auth

import (
	"net/http"
	"strings"
)

// A credential can arrive six ways, and only one of them is the one clients
// should use.
//
// Six spellings is six things to keep working, six things to get wrong, and six
// places a credential can end up somewhere it should not — a key in a query
// string is in the access log, in the Referer of the next request the page
// makes, and in browser history. The extra forms are still accepted, because
// removing them breaks every client at once, but a request that uses one is now
// told so on the response and recorded once per namespace so an operator can
// see who still has to move.

// KeyForm is how a credential arrived.
type KeyForm string

const (
	// KeyFormNone means no credential was presented.
	KeyFormNone KeyForm = ""

	// KeyFormBearer is the one form clients should use.
	KeyFormBearer KeyForm = "authorization-bearer"

	// KeyFormQuery is the WebSocket query token. A browser cannot set a header
	// on an upgrade, so this one is not going away.
	KeyFormQuery KeyForm = "query"

	// KeyFormHeader is `X-API-Key`.
	KeyFormHeader KeyForm = "x-api-key"

	// KeyFormApiKeyScheme is `Authorization: ApiKey <token>`.
	KeyFormApiKeyScheme KeyForm = "authorization-apikey"

	// KeyFormBare is `Authorization: <token>`, with no scheme at all.
	KeyFormBare KeyForm = "authorization-bare"
)

// Deprecated reports whether a form is one clients should stop using.
func (f KeyForm) Deprecated() bool {
	switch f {
	case KeyFormHeader, KeyFormApiKeyScheme, KeyFormBare:
		return true
	}
	return false
}

// APIKeyFromRequest reads the API key a request presents.
//
// The query string is read only on a WebSocket upgrade, where a browser cannot
// set a header. Everywhere else a credential in a URL ends up in the access
// log, in the Referer of the next request the page makes, and in history.
func APIKeyFromRequest(r *http.Request, allowQueryString bool) string {
	key, _ := APIKeyAndFormFromRequest(r, allowQueryString)
	return key
}

// APIKeyAndFormFromRequest reads the key and says which spelling it arrived in.
func APIKeyAndFormFromRequest(r *http.Request, allowQueryString bool) (string, KeyForm) {
	// The most explicit form wins.
	if v := strings.TrimSpace(r.Header.Get("X-API-Key")); v != "" {
		return v, KeyFormHeader
	}

	if header := r.Header.Get("Authorization"); header != "" {
		lower := strings.ToLower(header)
		switch {
		case strings.HasPrefix(lower, "bearer "):
			// A Bearer token with two dots is a JWT and is somebody else's to
			// verify. No key format can contain a dot — see
			// TestKeyFormatsContainNoDot — so this tells them apart exactly.
			if tok := strings.TrimSpace(header[len("Bearer "):]); strings.Count(tok, ".") != 2 {
				return tok, KeyFormBearer
			}
		case strings.HasPrefix(lower, "apikey "):
			if tok := strings.TrimSpace(header[len("ApiKey "):]); tok != "" {
				return tok, KeyFormApiKeyScheme
			}
		case !strings.Contains(header, " "):
			// No scheme at all: the whole value is the token, unless it is a
			// JWT.
			if tok := strings.TrimSpace(header); strings.Count(tok, ".") != 2 {
				return tok, KeyFormBare
			}
		}
	}

	if !allowQueryString {
		return "", KeyFormNone
	}
	if v := strings.TrimSpace(r.URL.Query().Get("api_key")); v != "" {
		return v, KeyFormQuery
	}
	if v := strings.TrimSpace(r.URL.Query().Get("token")); v != "" {
		return v, KeyFormQuery
	}
	return "", KeyFormNone
}
