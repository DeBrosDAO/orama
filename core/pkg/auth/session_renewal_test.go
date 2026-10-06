package auth

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

// A renewal that fails is not a session that ended. Only the gateway refusing
// the refresh token ends it; an outage, a restart or a busy gateway leaves the
// session as it was, so the next attempt can succeed.

// answeringGateway answers every refresh with one status and body.
func answeringGateway(t *testing.T, status int, body string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
		fmt.Fprint(w, body)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// expiredSession is a signed-in wallet whose access token has run out.
func expiredSession(refresh string) *Credentials {
	return &Credentials{
		Wallet:               "0xabc",
		Namespace:            "anchat",
		AccessToken:          "expired",
		AccessTokenExpiresAt: time.Now().Add(-time.Minute),
		RefreshToken:         refresh,
	}
}

func TestBearer_transportErrorKeepsTheSession(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	gatewayURL := srv.URL
	srv.Close() // nothing listens there any more
	creds := expiredSession("still-valid")

	_, err := Bearer(gatewayURL, nil, creds)
	if err == nil {
		t.Fatal("an unreachable gateway produced a token")
	}
	var urlErr *url.Error
	if !errors.As(err, &urlErr) {
		t.Errorf("the transport error was not kept in the chain: %v", err)
	}
	if strings.Contains(err.Error(), "session has ended") {
		t.Errorf("a network failure was reported as an ended session: %v", err)
	}
	if creds.RefreshToken != "still-valid" {
		t.Errorf("refresh token = %q after a network failure, want it kept", creds.RefreshToken)
	}
}

func TestBearer_gatewayFailureKeepsTheSession(t *testing.T) {
	for _, status := range []int{
		http.StatusInternalServerError,
		http.StatusBadGateway,
		http.StatusServiceUnavailable,
		http.StatusTooManyRequests,
		http.StatusBadRequest,
	} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			srv := answeringGateway(t, status, `{"error":"refresh temporarily unavailable, retry"}`)
			creds := expiredSession("still-valid")

			_, err := Bearer(srv.URL, nil, creds)
			var gwErr *GatewayError
			if !errors.As(err, &gwErr) || gwErr.Status != status {
				t.Fatalf("err = %v, want the gateway's HTTP %d in the chain", err, status)
			}
			if strings.Contains(err.Error(), "session has ended") {
				t.Errorf("HTTP %d was reported as an ended session: %v", status, err)
			}
			if creds.RefreshToken != "still-valid" {
				t.Errorf("refresh token = %q after HTTP %d, want it kept", creds.RefreshToken, status)
			}
		})
	}
}

// A gateway that is down must not be answered by sending the API key instead:
// the exchange would fail the same way, and the key is the credential that
// should travel least.
func TestBearer_gatewayFailureDoesNotFallBackToTheKey(t *testing.T) {
	exchanges := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/auth/token" {
			exchanges++
		}
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	t.Cleanup(srv.Close)
	creds := expiredSession("still-valid")
	creds.APIKey = "orama_sk_key_x"

	if _, err := Bearer(srv.URL, nil, creds); err == nil {
		t.Fatal("a failing gateway produced a token")
	}
	if exchanges != 0 {
		t.Errorf("the key was exchanged %d times while the gateway was failing", exchanges)
	}
}

func TestBearer_rejectedRefreshTokenEndsTheSession(t *testing.T) {
	for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			srv := answeringGateway(t, status, `{"error":"invalid or expired refresh token"}`)
			creds := expiredSession("spent")

			_, err := Bearer(srv.URL, nil, creds)
			if err == nil || !strings.Contains(err.Error(), "session has ended") {
				t.Fatalf("err = %v, want the session to have ended", err)
			}
			if !errors.Is(err, ErrSessionEnded) {
				t.Errorf("err = %v, want ErrSessionEnded for the caller to classify", err)
			}
			if creds.RefreshToken != "" {
				t.Errorf("the refused refresh token was kept: %q", creds.RefreshToken)
			}
		})
	}
}

// The ended session is written to disk, so the next command says so without
// presenting the dead token again.
func TestBearer_rejectedRefreshTokenIsForgottenOnDisk(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	srv := answeringGateway(t, http.StatusUnauthorized, `{"error":"invalid or expired refresh token"}`)
	store, creds := storeSession(t, srv.URL, expiredSession("spent"))

	if _, err := Bearer(srv.URL, store, creds); err == nil {
		t.Fatal("a refused refresh produced a token")
	}
	if got := reloadSession(t, srv.URL); got.RefreshToken != "" {
		t.Errorf("stored refresh token = %q, want it forgotten", got.RefreshToken)
	}
}

func TestBearer_transportErrorLeavesTheStoredSessionAlone(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	srv := httptest.NewServer(http.NotFoundHandler())
	gatewayURL := srv.URL
	srv.Close()
	store, creds := storeSession(t, gatewayURL, expiredSession("still-valid"))

	if _, err := Bearer(gatewayURL, store, creds); err == nil {
		t.Fatal("an unreachable gateway produced a token")
	}
	if got := reloadSession(t, gatewayURL); got.RefreshToken != "still-valid" {
		t.Errorf("stored refresh token = %q after a network failure, want it kept", got.RefreshToken)
	}
}

func TestRefreshTokenRejected(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"401", &GatewayError{Status: http.StatusUnauthorized}, true},
		{"403 wrapped", fmt.Errorf("refresh: %w", &GatewayError{Status: http.StatusForbidden}), true},
		{"400", &GatewayError{Status: http.StatusBadRequest}, false},
		{"429", &GatewayError{Status: http.StatusTooManyRequests}, false},
		{"503", &GatewayError{Status: http.StatusServiceUnavailable}, false},
		{"transport", &url.Error{Op: "Post", URL: "https://gw", Err: errors.New("connection refused")}, false},
		{"nil", nil, false},
	}
	for _, c := range cases {
		if got := refreshTokenRejected(c.err); got != c.want {
			t.Errorf("%s: refreshTokenRejected = %v, want %v", c.name, got, c.want)
		}
	}
}
