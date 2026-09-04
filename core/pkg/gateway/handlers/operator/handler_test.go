package operator

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/gateway/auth"
	"github.com/DeBrosOfficial/network/pkg/gateway/ctxkeys"
)

func TestWalletFromRequest_withClaims(t *testing.T) {
	h := NewHandler(nil, nil)
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	claims := &auth.JWTClaims{Sub: "0xabc123"}
	ctx := context.WithValue(r.Context(), ctxkeys.JWT, claims)
	r = r.WithContext(ctx)

	wallet := h.walletFromRequest(r)
	if wallet != "0xabc123" {
		t.Errorf("wallet = %q, want %q", wallet, "0xabc123")
	}
}

func TestWalletFromRequest_noClaims(t *testing.T) {
	h := NewHandler(nil, nil)
	r := httptest.NewRequest(http.MethodGet, "/", nil)

	wallet := h.walletFromRequest(r)
	if wallet != "" {
		t.Errorf("wallet = %q, want empty", wallet)
	}
}

func TestWalletFromRequest_nilClaims(t *testing.T) {
	h := NewHandler(nil, nil)
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	ctx := context.WithValue(r.Context(), ctxkeys.JWT, (*auth.JWTClaims)(nil))
	r = r.WithContext(ctx)

	wallet := h.walletFromRequest(r)
	if wallet != "" {
		t.Errorf("wallet = %q, want empty", wallet)
	}
}

func TestWalletFromRequest_apiKeyContext(t *testing.T) {
	// When auth middleware sets ctxkeys.APIKey (no JWT), walletFromRequest
	// should try to resolve via the API key. With nil rqliteClient it returns
	// empty (can't query DB), but it shouldn't panic.
	h := NewHandler(nil, nil)
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	ctx := context.WithValue(r.Context(), ctxkeys.APIKey, "ak_test:myns")
	r = r.WithContext(ctx)

	// Should not panic — returns empty because no DB to query
	wallet := h.walletFromRequest(r)
	if wallet != "" {
		t.Errorf("wallet = %q, want empty (no DB)", wallet)
	}
}

// The namespace used to be parsed out of the key string — `ak_<random>:<ns>`.
// A key does not carry one now, so it comes from the token's claim or from what
// the middleware resolved, and a caller with neither is nobody.
func TestResolveWallet_takesTheNamespaceFromTheCredentialNotTheKeyString(t *testing.T) {
	h := NewHandler(nil, nil)

	// A wallet JWT is its own answer.
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r = r.WithContext(context.WithValue(r.Context(), ctxkeys.JWT,
		&auth.JWTClaims{Sub: "0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}))
	if got := h.resolveWallet(r); got != "0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb" {
		t.Errorf("a wallet JWT resolved to %q", got)
	}

	// A key-exchanged JWT carries its namespace as a claim. No database here,
	// so the lookup answers empty — what matters is that it did not read a
	// namespace out of the subject.
	r = httptest.NewRequest(http.MethodGet, "/", nil)
	r = r.WithContext(context.WithValue(r.Context(), ctxkeys.JWT,
		&auth.JWTClaims{Sub: "orama_sk_abc_def", Namespace: "myns"}))
	if got := h.resolveWallet(r); got != "" {
		t.Errorf("resolveWallet = %q with no database", got)
	}

	// Nothing at all.
	if got := h.resolveWallet(httptest.NewRequest(http.MethodGet, "/", nil)); got != "" {
		t.Errorf("a request with no credential resolved to %q", got)
	}
}

func TestDecodeJSON_valid(t *testing.T) {
	body := strings.NewReader(`{"node_id":"test-node","environment":"devnet"}`)
	r := httptest.NewRequest(http.MethodPost, "/", body)

	var req RegisterRequest
	if err := decodeJSON(r, &req); err != nil {
		t.Fatalf("decodeJSON: %v", err)
	}
	if req.NodeID != "test-node" {
		t.Errorf("NodeID = %q, want %q", req.NodeID, "test-node")
	}
	if req.Environment != "devnet" {
		t.Errorf("Environment = %q, want %q", req.Environment, "devnet")
	}
}

func TestDecodeJSON_invalid(t *testing.T) {
	body := strings.NewReader(`not-json`)
	r := httptest.NewRequest(http.MethodPost, "/", body)

	var req RegisterRequest
	if err := decodeJSON(r, &req); err == nil {
		t.Error("expected error for invalid JSON")
	}
}

func TestHandleInvite_noAuth(t *testing.T) {
	h := NewHandler(nil, nil)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/v1/operator/invite", nil)

	h.HandleInvite(w, r)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", w.Code, http.StatusUnauthorized)
	}
}

func TestHandleInvite_wrongMethod(t *testing.T) {
	h := NewHandler(nil, nil)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/v1/operator/invite", nil)

	h.HandleInvite(w, r)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want %d", w.Code, http.StatusMethodNotAllowed)
	}
}

func TestHandleListNodes_noAuth(t *testing.T) {
	h := NewHandler(nil, nil)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/v1/operator/nodes", nil)

	h.HandleListNodes(w, r)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", w.Code, http.StatusUnauthorized)
	}
}

func TestHandleListNodes_wrongMethod(t *testing.T) {
	h := NewHandler(nil, nil)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/v1/operator/nodes", nil)

	h.HandleListNodes(w, r)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want %d", w.Code, http.StatusMethodNotAllowed)
	}
}

func TestHandleRegister_noAuth(t *testing.T) {
	h := NewHandler(nil, nil)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/v1/operator/node/register", strings.NewReader(`{"node_id":"test"}`))

	h.HandleRegister(w, r)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", w.Code, http.StatusUnauthorized)
	}
}

func TestHandleRegister_missingFields(t *testing.T) {
	// Authorization runs first now, so the caller has to be an operator before
	// the request body is looked at: an unauthorized caller should not learn
	// which inputs are valid.
	h, _ := operatorHandler(t, "0xabc")
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/v1/operator/node/register", strings.NewReader(`{}`))
	claims := &auth.JWTClaims{Sub: "0xabc"}
	r = r.WithContext(context.WithValue(r.Context(), ctxkeys.JWT, claims))

	h.HandleRegister(w, r)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestHandleRegister_invalidEnvironment(t *testing.T) {
	h, _ := operatorHandler(t, "0xabc")
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/v1/operator/node/register",
		strings.NewReader(`{"node_id":"test","environment":"<script>alert(1)</script>"}`))
	claims := &auth.JWTClaims{Sub: "0xabc"}
	r = r.WithContext(context.WithValue(r.Context(), ctxkeys.JWT, claims))

	h.HandleRegister(w, r)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestHandleRegister_invalidRole(t *testing.T) {
	h, _ := operatorHandler(t, "0xabc")
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/v1/operator/node/register",
		strings.NewReader(`{"node_id":"test","role":"admin"}`))
	claims := &auth.JWTClaims{Sub: "0xabc"}
	r = r.WithContext(context.WithValue(r.Context(), ctxkeys.JWT, claims))

	h.HandleRegister(w, r)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestAllowedEnvironments(t *testing.T) {
	valid := []string{"devnet", "testnet", "sandbox", "production", "mainnet"}
	invalid := []string{"staging", "local", "<script>", ""}

	for _, env := range valid {
		if !allowedEnvironments[env] {
			t.Errorf("expected %q to be allowed", env)
		}
	}
	for _, env := range invalid {
		if allowedEnvironments[env] {
			t.Errorf("expected %q to be disallowed", env)
		}
	}
}

func TestAllowedRoles(t *testing.T) {
	valid := []string{"node", "nameserver", "nameserver-ns1", "nameserver-ns2", "nameserver-ns3"}
	invalid := []string{"admin", "root", ""}

	for _, role := range valid {
		if !allowedRoles[role] {
			t.Errorf("expected %q to be allowed", role)
		}
	}
	for _, role := range invalid {
		if allowedRoles[role] {
			t.Errorf("expected %q to be disallowed", role)
		}
	}
}
