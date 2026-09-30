package operator

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/pkg/gateway/auth"
	"github.com/DeBrosOfficial/network/pkg/gateway/ctxkeys"
)

// inviteRequest is an operator's invite request whose body has no declared
// length (-1), as a chunked or proxied body arrives.
func inviteRequest(body string) *http.Request {
	r := httptest.NewRequest(http.MethodPost, "/v1/operator/invite", strings.NewReader(body))
	r.ContentLength = -1
	return r.WithContext(context.WithValue(r.Context(), ctxkeys.JWT, &auth.JWTClaims{Sub: "0xoperator"}))
}

func inviteLifetime(t *testing.T, w *httptest.ResponseRecorder) time.Duration {
	t.Helper()
	var body InviteResponse
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	expires, err := time.Parse("2006-01-02 15:04:05", body.ExpiresAt)
	if err != nil {
		t.Fatalf("parse expiry %q: %v", body.ExpiresAt, err)
	}
	return time.Until(expires)
}

// A chunked body is read: its expiry applies. Judging "is there a body" by
// ContentLength > 0 ignored it and minted a 60-minute invite.
func TestHandleInvite_aChunkedBodyIsRead(t *testing.T) {
	h, _ := operatorHandler(t, "0xoperator")
	w := httptest.NewRecorder()
	h.HandleInvite(w, inviteRequest(`{"expiry_minutes": 5}`))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", w.Code, w.Body.String())
	}
	if got := inviteLifetime(t, w); got > 6*time.Minute {
		t.Errorf("invite lives %s, want the 5 minutes asked for", got.Round(time.Minute))
	}
}

func TestHandleInvite_anEmptyBodyTakesTheDefault(t *testing.T) {
	h, _ := operatorHandler(t, "0xoperator")
	w := httptest.NewRecorder()
	h.HandleInvite(w, inviteRequest(""))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", w.Code, w.Body.String())
	}
	if got := inviteLifetime(t, w); got < 59*time.Minute || got > 61*time.Minute {
		t.Errorf("invite lives %s, want the 60-minute default", got.Round(time.Minute))
	}
}

// A body that is not the request is refused, not silently replaced by the
// default, and nothing is minted.
func TestHandleInvite_aMalformedBodyIsRefused(t *testing.T) {
	h, db := operatorHandler(t, "0xoperator")
	w := httptest.NewRecorder()
	h.HandleInvite(w, inviteRequest(`{"expiry_minutes": "soon"`))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", w.Code, w.Body.String())
	}
	if len(db.writes) != 0 {
		t.Errorf("%d invites were stored for a refused request", len(db.writes))
	}
}
