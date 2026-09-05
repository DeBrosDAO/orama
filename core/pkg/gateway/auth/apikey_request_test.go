package auth

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAPIKeyFromRequest_headers(t *testing.T) {
	for _, tc := range []struct {
		name string
		set  func(*http.Request)
		want string
	}{
		{"x-api-key", func(r *http.Request) { r.Header.Set("X-API-Key", "ak_header") }, "ak_header"},
		{"bearer", func(r *http.Request) { r.Header.Set("Authorization", "Bearer ak_bearer") }, "ak_bearer"},
		{"apikey scheme", func(r *http.Request) { r.Header.Set("Authorization", "ApiKey ak_scheme") }, "ak_scheme"},
		{"lowercase scheme", func(r *http.Request) { r.Header.Set("Authorization", "apikey ak_scheme") }, "ak_scheme"},
		{"no scheme", func(r *http.Request) { r.Header.Set("Authorization", "ak_bare") }, "ak_bare"},
		{"nothing", func(r *http.Request) {}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			tc.set(req)
			if got := APIKeyFromRequest(req, false); got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

// A JWT is somebody else's to verify. Returning one here would have it looked
// up as an API key and refused, hiding the real credential.
func TestAPIKeyFromRequest_leavesJWTsAlone(t *testing.T) {
	jwt := "eyJhbGciOiJFZERTQSJ9.eyJzdWIiOiIweCJ9.c2ln"
	for _, header := range []string{"Bearer " + jwt, jwt} {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("Authorization", header)
		if got := APIKeyFromRequest(req, false); got != "" {
			t.Errorf("Authorization %q yielded %q", header, got)
		}
	}
}

// The header is the explicit form and wins, so a URL cannot displace it.
func TestAPIKeyFromRequest_theHeaderWinsOverTheQueryString(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/?api_key=ak_url", nil)
	req.Header.Set("X-API-Key", "ak_header")
	if got := APIKeyFromRequest(req, true); got != "ak_header" {
		t.Fatalf("got %q, want the header's key", got)
	}
}

// The whole point of the parameter. A credential in a URL reaches the access
// log, the Referer of whatever the page loads next, and the browser's history,
// so it is read only where a header genuinely cannot be sent.
func TestAPIKeyFromRequest_theQueryStringIsReadOnlyWhenAllowed(t *testing.T) {
	for _, target := range []string{"/?api_key=ak_url", "/?token=ak_url"} {
		req := httptest.NewRequest(http.MethodGet, target, nil)

		if got := APIKeyFromRequest(req, false); got != "" {
			t.Errorf("%s: took a key from the URL when it was not allowed: %q", target, got)
		}
		if got := APIKeyFromRequest(req, true); got != "ak_url" {
			t.Errorf("%s: did not take the key when it was allowed: %q", target, got)
		}
	}
}

func TestAPIKeyFromRequest_trimsWhitespace(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-API-Key", "  ak_padded  ")
	if got := APIKeyFromRequest(req, false); got != "ak_padded" {
		t.Fatalf("got %q", got)
	}

	blank := httptest.NewRequest(http.MethodGet, "/", nil)
	blank.Header.Set("X-API-Key", "   ")
	blank.Header.Set("Authorization", "ApiKey ak_fallback")
	if got := APIKeyFromRequest(blank, false); got != "ak_fallback" {
		t.Fatalf("a blank X-API-Key stopped the Authorization header being read: %q", got)
	}
}

// A key whose stored scopes are empty grants nothing. It used to grant admin:
// an empty column was read as "no restriction", so a key minted before scopes
// existed — or minted with none — was a control-plane credential.
func TestScopesFromStored_anEmptyColumnGrantsNothing(t *testing.T) {
	for _, stored := range []string{"", "   ", ",", " , ,"} {
		got := ScopesFromStored(stored)
		if len(got) != 0 {
			t.Errorf("ScopesFromStored(%q) = %v, want an empty set", stored, got)
		}
		if got.Has(ScopeAdmin) {
			t.Errorf("ScopesFromStored(%q) grants admin", stored)
		}
	}
}

// A Bearer token is a JWT if it has exactly two dots, and a key otherwise. That
// is only sound while no key format can contain a dot: a key with two in it
// would be handed to the JWT verifier and refused as a bad token, and a JWT
// with any other number would be looked up as a key.
//
// Both key formats are checked here rather than reasoned about: `orama_…` is
// base62 with underscores, and the legacy `ak_<payload>:<namespace>` is
// base64url with a colon. Neither alphabet has a dot, and this fails if one
// ever gains it.
func TestKeyFormatsContainNoDot(t *testing.T) {
	if strings.Contains(base62Alphabet, ".") {
		t.Error("the key alphabet contains a dot, so a key can be mistaken for a JWT")
	}
	if strings.Contains(KeyPrefix, ".") {
		t.Error("the key prefix contains a dot")
	}

	for i := 0; i < 200; i++ {
		key, err := NewKey(KeyTypeService)
		if err != nil {
			t.Fatalf("NewKey: %v", err)
		}
		if strings.Contains(key, ".") {
			t.Fatalf("minted key %q contains a dot", key)
		}
		req := httptest.NewRequest(http.MethodGet, "/v1/cache/get", nil)
		req.Header.Set("Authorization", "Bearer "+key)
		if got, form := APIKeyAndFormFromRequest(req, false); got != key || form != KeyFormBearer {
			t.Fatalf("a minted key sent as Bearer read back as (%q, %q)", got, form)
		}
	}

	// The legacy format is base64url plus a colon.
	for _, legacy := range []string{"ak_abcDEF-123_xyz:acme", "ak_A:default"} {
		if strings.Contains(legacy, ".") {
			t.Fatalf("legacy key %q contains a dot", legacy)
		}
	}
}
