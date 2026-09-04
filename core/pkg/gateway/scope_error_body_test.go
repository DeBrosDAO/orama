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
