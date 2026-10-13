package cli

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/auth"
)

// logoutRecorder is a gateway that answers /v1/auth/logout with status and
// remembers what it was sent.
type logoutRecorder struct {
	*httptest.Server
	authorization string
	body          map[string]any
}

func newLogoutRecorder(t *testing.T, status int) *logoutRecorder {
	t.Helper()
	rec := &logoutRecorder{}
	rec.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/auth/logout" || r.Method != http.MethodPost {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		rec.authorization = r.Header.Get("Authorization")
		_ = json.NewDecoder(r.Body).Decode(&rec.body)
		w.WriteHeader(status)
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	}))
	t.Cleanup(rec.Close)
	return rec
}

// The refresh token in the body stops a new access token being bought; it does
// nothing to the one this machine holds, which the gateway can only end if it
// is told whose it is. `orama auth logout` sent no Authorization header, so the
// token it had been using kept working until it expired.
func TestEndSessionOnGateway_presentsTheAccessTokenItHolds(t *testing.T) {
	rec := newLogoutRecorder(t, http.StatusOK)
	creds := &auth.Credentials{AccessToken: "held-access", RefreshToken: "held-refresh", Namespace: "anchat"}

	if err := endSessionOnGateway(rec.URL, nil, creds, false); err != nil {
		t.Fatalf("logout: %v", err)
	}
	if rec.authorization != "Bearer held-access" {
		t.Errorf("Authorization = %q, want the held access token", rec.authorization)
	}
	if rec.body["refresh_token"] != "held-refresh" || rec.body["namespace"] != "anchat" || rec.body["all"] != false {
		t.Errorf("body = %v", rec.body)
	}
}

// A machine holding only a refresh token (its access token was never stored or
// has been cleared) still ends what it can: the refresh token.
func TestEndSessionOnGateway_withNoAccessTokenStillRevokesTheRefreshToken(t *testing.T) {
	rec := newLogoutRecorder(t, http.StatusOK)
	creds := &auth.Credentials{RefreshToken: "held-refresh", Namespace: "anchat"}

	if err := endSessionOnGateway(rec.URL, nil, creds, false); err != nil {
		t.Fatalf("logout: %v", err)
	}
	if rec.authorization != "" {
		t.Errorf("Authorization = %q, want none: there was no token to present", rec.authorization)
	}
	if rec.body["refresh_token"] != "held-refresh" {
		t.Errorf("body = %v", rec.body)
	}
}

func TestEndSessionOnGateway_aRefusalIsAnError(t *testing.T) {
	rec := newLogoutRecorder(t, http.StatusInternalServerError)
	creds := &auth.Credentials{AccessToken: "held-access", RefreshToken: "held-refresh"}

	if err := endSessionOnGateway(rec.URL, nil, creds, false); err == nil {
		t.Fatal("a gateway that failed to end the session was reported as having ended it")
	}
}
