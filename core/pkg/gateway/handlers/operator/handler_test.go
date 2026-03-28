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

func TestExtractNamespace(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"ak_abc123:myns", "myns"},
		{"ak_abc123", "ak_abc123"},
		{"", ""},
	}
	for _, tt := range tests {
		got := extractNamespace(tt.input)
		if got != tt.want {
			t.Errorf("extractNamespace(%q) = %q, want %q", tt.input, got, tt.want)
		}
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
	h := NewHandler(nil, nil)
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
	h := NewHandler(nil, nil)
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
	h := NewHandler(nil, nil)
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
