package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/gateway/auth"
	"github.com/DeBrosOfficial/network/pkg/logging"
)

// A client that is refused for lack of a grant has to be able to act on it:
// which grant, so it can ask for a key that has it. The refusal used to carry
// the grant only inside an English sentence, so the only way to read it was to
// match the prose — and prose changes.
func TestInsufficientScopeNamesTheGrantInAField(t *testing.T) {
	rec := serveWithScopes(t, http.MethodPost, "/v1/storage/upload", auth.ScopeSet{auth.ScopeInvoke: {}})

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}

	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("response is not JSON: %v (%s)", err, rec.Body.String())
	}

	if body["code"] != "INSUFFICIENT_SCOPE" {
		t.Errorf("code = %v, want INSUFFICIENT_SCOPE", body["code"])
	}
	if body["required_scope"] != auth.ScopeStorage {
		t.Errorf("required_scope = %v, want %q", body["required_scope"], auth.ScopeStorage)
	}
	// The prose stays, so nothing that reads `error` today breaks.
	msg, ok := body["error"].(string)
	if !ok || msg == "" {
		t.Fatalf("error must remain a non-empty string, got %v", body["error"])
	}
	if !strings.Contains(msg, auth.ScopeStorage) {
		t.Errorf("message does not mention the grant: %s", msg)
	}
}

// The same for the layer-1 refusal: an API key alone on a data-plane grant that
// requires a logged-in user.
func TestUserJWTRequiredNamesTheGrantInAField(t *testing.T) {
	rec := serveWithScopes(t, http.MethodPost, "/v1/storage/upload", auth.ScopeSet{auth.ScopeStorage: {}})

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}

	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("response is not JSON: %v (%s)", err, rec.Body.String())
	}
	if body["code"] != "USER_JWT_REQUIRED" {
		t.Errorf("code = %v, want USER_JWT_REQUIRED", body["code"])
	}
	if body["required_scope"] != auth.ScopeStorage {
		t.Errorf("required_scope = %v, want %q", body["required_scope"], auth.ScopeStorage)
	}
}

// A caller that holds the grant is not touched by the middleware.
func TestSufficientScopePassesThrough(t *testing.T) {
	rec := serveWithScopes(t, http.MethodPost, "/v1/pubsub/publish", auth.ScopeSet{auth.ScopePubsub: {}})

	if rec.Code != http.StatusTeapot {
		t.Fatalf("status = %d, want the handler's 418 — the request did not reach it: %s", rec.Code, rec.Body.String())
	}
}

func serveWithScopes(t *testing.T, method, path string, scopes auth.ScopeSet) *httptest.ResponseRecorder {
	t.Helper()

	logger, err := logging.NewColoredLogger(logging.ComponentGateway, false)
	if err != nil {
		t.Fatalf("logger: %v", err)
	}
	g := &Gateway{logger: logger}

	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	})

	r := httptest.NewRequest(method, path, nil)
	r = r.WithContext(context.WithValue(r.Context(), ctxKeyScopes, scopes))

	rec := httptest.NewRecorder()
	g.scopeMiddleware(next).ServeHTTP(rec, r)
	return rec
}

// Creating a namespace requires a logged-in wallet and no grant at all: a
// wallet with no namespace holds a grant nowhere, so requiring one would mean
// nobody could ever start.
//
// The token check used to be reached only after a grant had been checked, so a
// route declaring a token requirement and no grant got neither. That made the
// requirement silent, which is worse than absent.
func TestNamespaceCreation_needsAWalletTokenAndNoGrant(t *testing.T) {
	if policy := policyOf(http.MethodPost, "/v1/namespaces"); policy.Domain != "" {
		t.Fatalf("/v1/namespaces requires the %q grant; this test is about the case where it requires none", policy.Domain)
	}

	t.Run("a bare key is refused", func(t *testing.T) {
		rec := serveWithScopes(t, http.MethodPost, "/v1/namespaces", auth.ScopeSet{auth.ScopePubsub: {}})

		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want %d — a key created a namespace: %s",
				rec.Code, http.StatusUnauthorized, rec.Body.String())
		}
		var body map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if body["code"] != "USER_JWT_REQUIRED" {
			t.Errorf("code = %v, want USER_JWT_REQUIRED", body["code"])
		}
	})

	t.Run("an exchanged-key token is refused too", func(t *testing.T) {
		rec := serveAsSubject(t, "/v1/namespaces", "orama_sk_payload_check", auth.ScopeSet{})

		if rec.Code != http.StatusUnauthorized {
			t.Errorf("status = %d, want %d — a key proved possession of itself and created a namespace",
				rec.Code, http.StatusUnauthorized)
		}
	})

	t.Run("a signed-in wallet passes", func(t *testing.T) {
		rec := serveAsSubject(t, "/v1/namespaces", "0xowner", auth.ScopeSet{})

		if rec.Code != http.StatusTeapot {
			t.Errorf("status = %d, want the handler's 418 — a wallet with no grant could not create a namespace: %s",
				rec.Code, rec.Body.String())
		}
	})
}

// serveAsSubject runs the scope middleware for a caller carrying a JWT with the
// given subject.
func serveAsSubject(t *testing.T, path, subject string, scopes auth.ScopeSet) *httptest.ResponseRecorder {
	t.Helper()

	logger, err := logging.NewColoredLogger(logging.ComponentGateway, false)
	if err != nil {
		t.Fatalf("logger: %v", err)
	}
	g := &Gateway{logger: logger}

	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	})

	r := httptest.NewRequest(http.MethodPost, path, nil)
	ctx := context.WithValue(r.Context(), ctxKeyScopes, scopes)
	ctx = context.WithValue(ctx, ctxKeyJWT, &auth.JWTClaims{Sub: subject})
	rec := httptest.NewRecorder()

	g.scopeMiddleware(next).ServeHTTP(rec, r.WithContext(ctx))
	return rec
}
