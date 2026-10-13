package auth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	authsvc "github.com/DeBrosOfficial/network/pkg/gateway/auth"
)

// The polling client switches on the `error` member and nothing else, so the
// mapping from outcome to wire name is the contract. RFC 8628 §3.5 names all
// four, and a client that sees an unrecognised one has to guess.
func TestWriteDeviceError_saysWhatTheClientSwitchesOn(t *testing.T) {
	for _, tc := range []struct {
		err    error
		status int
		code   string
	}{
		{authsvc.ErrDeviceAuthorizationPending, http.StatusBadRequest, "authorization_pending"},
		{authsvc.ErrDeviceSlowDown, http.StatusBadRequest, "slow_down"},
		{authsvc.ErrDeviceCodeExpired, http.StatusBadRequest, "expired_token"},
		{authsvc.ErrDeviceAccessDenied, http.StatusBadRequest, "access_denied"},
		{authsvc.ErrDeviceCodeUnknown, http.StatusBadRequest, "invalid_grant"},
		{authsvc.ErrDeviceAlreadyApproved, http.StatusConflict, "already_approved"},
	} {
		t.Run(tc.code, func(t *testing.T) {
			rec := httptest.NewRecorder()
			writeDeviceError(rec, tc.err)

			if rec.Code != tc.status {
				t.Errorf("status = %d, want %d", rec.Code, tc.status)
			}
			var body map[string]any
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode: %v (%s)", err, rec.Body.String())
			}
			if got, _ := body["error"].(string); got != tc.code {
				t.Errorf("error = %q, want %q", got, tc.code)
			}
			// One word is not enough for somebody reading a failed poll with
			// curl, and the description is the only place the next step goes.
			if desc, _ := body["error_description"].(string); strings.TrimSpace(desc) == "" {
				t.Error("no error_description")
			}
		})
	}
}

// Every one of these takes a POST. A GET that fell through to the body decode
// would report "invalid json body" for what is actually the wrong method.
func TestDeviceHandlers_refuseAnythingButPost(t *testing.T) {
	h := whoamiHandlers(t)
	for name, handler := range map[string]http.HandlerFunc{
		"/v1/auth/device":         h.DeviceAuthorizationHandler,
		"/v1/auth/device/approve": h.DeviceApprovalHandler,
		"/v1/auth/device/token":   h.DeviceTokenHandler,
	} {
		t.Run(name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			handler(rec, httptest.NewRequest(http.MethodGet, name, nil))
			if rec.Code != http.StatusMethodNotAllowed {
				t.Errorf("GET %s = %d, want %d", name, rec.Code, http.StatusMethodNotAllowed)
			}
		})
	}
}

// A code is the whole of what the approver is acting on, and approving an empty
// one would mean approving whichever pending login the query happened to find.
func TestDeviceApprovalHandler_requiresAUserCode(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/auth/device/approve",
		strings.NewReader(`{"message":"m","signature":"s"}`))

	whoamiHandlers(t).DeviceApprovalHandler(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
	if !strings.Contains(rec.Body.String(), "user_code") {
		t.Errorf("the refusal does not name what is missing: %s", rec.Body.String())
	}
}

// Sessions belong to a wallet. An API key is a credential in its own right and
// names no wallet, so it must not reach the sessions of whoever minted it.
func TestSessionsHandler_refusesAnAPIKeyCredential(t *testing.T) {
	h := whoamiHandlers(t)

	for name, call := range map[string]func(http.ResponseWriter, *http.Request){
		"list": h.SessionsHandler,
		"end":  h.SessionByIDHandler,
	} {
		t.Run(name, func(t *testing.T) {
			method := http.MethodGet
			path := "/v1/auth/sessions"
			if name == "end" {
				method, path = http.MethodDelete, "/v1/auth/sessions/1"
			}
			req := httptest.NewRequest(method, path, nil)
			ctx := context.WithValue(req.Context(), CtxKeyAPIKey, "orama_sk_whatever_x")
			rec := httptest.NewRecorder()

			call(rec, req.WithContext(ctx))

			if rec.Code != http.StatusForbidden {
				t.Fatalf("status = %d, want %d", rec.Code, http.StatusForbidden)
			}
			if !strings.Contains(rec.Body.String(), "orama auth login") {
				t.Errorf("the refusal does not say what to do instead: %s", rec.Body.String())
			}
		})
	}
}

func TestSessionByIDHandler_refusesAPathWithNoUsableID(t *testing.T) {
	h := whoamiHandlers(t)
	claims := &authsvc.JWTClaims{Sub: "0xowner", Namespace: "anchat"}

	for _, path := range []string{"/v1/auth/sessions/", "/v1/auth/sessions/all", "/v1/auth/sessions/0", "/v1/auth/sessions/-3"} {
		t.Run(path, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodDelete, path, nil)
			ctx := context.WithValue(req.Context(), CtxKeyJWT, claims)
			rec := httptest.NewRecorder()

			h.SessionByIDHandler(rec, req.WithContext(ctx))

			if rec.Code != http.StatusBadRequest {
				t.Errorf("DELETE %s = %d, want %d", path, rec.Code, http.StatusBadRequest)
			}
		})
	}
}
