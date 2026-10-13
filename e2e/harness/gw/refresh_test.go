package gw

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestSessionStale_byLifetime(t *testing.T) {
	now := time.Now()
	fresh := &Session{RefreshToken: "r", ExpiresIn: 900, issuedAt: now.Add(-5 * time.Minute)}
	old := &Session{RefreshToken: "r", ExpiresIn: 900, issuedAt: now.Add(-11 * time.Minute)}
	if fresh.Stale(now) {
		t.Error("a session 5 of 15 minutes in is stale")
	}
	if !old.Stale(now) {
		t.Error("a session 11 of 15 minutes in is not stale")
	}
}

func TestSessionStale_nothingToRefreshWith(t *testing.T) {
	long := time.Now().Add(-time.Hour)
	for name, s := range map[string]*Session{
		"nil":             nil,
		"no refresh":      {ExpiresIn: 900, issuedAt: long},
		"no lifetime":     {RefreshToken: "r", issuedAt: long},
		"not issued here": {RefreshToken: "r", ExpiresIn: 900},
	} {
		if s.Stale(time.Now()) {
			t.Errorf("%s: reported stale", name)
		}
	}
}

// refreshServer answers POST /v1/auth/refresh with a rotated session, or with
// status when it is not 200, and counts the calls.
func refreshServer(t *testing.T, status int) (*Client, *atomic.Int32) {
	t.Helper()
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != PathRefresh {
			http.NotFound(w, r)
			return
		}
		n := calls.Add(1)
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["refresh_token"] != "refresh-0" || body["namespace"] != "tenant" {
			http.Error(w, "unexpected body", http.StatusBadRequest)
			return
		}
		if status != http.StatusOK {
			http.Error(w, `{"code":"AUTH_INVALID"}`, status)
			return
		}
		_ = json.NewEncoder(w).Encode(Session{AccessToken: "access-" + string(rune('0'+n)), RefreshToken: "refresh-1",
			ExpiresIn: 900, Namespace: "tenant"})
	}))
	t.Cleanup(srv.Close)
	c, err := NewWithTLS(srv.URL, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	return c, &calls
}

func staleUser(c *Client) *User {
	return &User{Client: c, Namespace: "tenant", Session: &Session{AccessToken: "access-0", RefreshToken: "refresh-0",
		ExpiresIn: 900, Namespace: "tenant", issuedAt: time.Now().Add(-14 * time.Minute)}}
}

func TestUserToken_refreshesAStaleSession(t *testing.T) {
	c, calls := refreshServer(t, http.StatusOK)
	u := staleUser(c)
	if got := u.Token(); got != "access-1" {
		t.Fatalf("Token() = %q, want the refreshed access-1", got)
	}
	if got := u.Token(); got != "access-1" || calls.Load() != 1 {
		t.Fatalf("second Token() = %q after %d refreshes, want access-1 after 1: a fresh session was refreshed again", got, calls.Load())
	}
}

func TestUserToken_freshSessionNotRefreshed(t *testing.T) {
	c, calls := refreshServer(t, http.StatusOK)
	u := staleUser(c)
	u.Session.issuedAt = time.Now()
	if got := u.Token(); got != "access-0" || calls.Load() != 0 {
		t.Fatalf("Token() = %q after %d refreshes, want access-0 with none", got, calls.Load())
	}
}

func TestUserToken_failedRefreshKeepsTheSession(t *testing.T) {
	c, calls := refreshServer(t, http.StatusUnauthorized)
	u := staleUser(c)
	if got := u.Token(); got != "access-0" {
		t.Fatalf("Token() after a refused refresh = %q, want the old access-0", got)
	}
	if calls.Load() != 1 || u.Session.RefreshToken != "refresh-0" {
		t.Fatalf("refresh calls %d, refresh token %q: a refused refresh replaced the session", calls.Load(), u.Session.RefreshToken)
	}
}

func TestUserToken_concurrentCallersRefreshOnce(t *testing.T) {
	c, calls := refreshServer(t, http.StatusOK)
	u := staleUser(c)
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if got := u.Token(); got != "access-1" {
				t.Errorf("concurrent Token() = %q, want access-1", got)
			}
		}()
	}
	wg.Wait()
	if calls.Load() != 1 {
		t.Fatalf("%d refreshes for one stale session: the rotated refresh token would be spent twice", calls.Load())
	}
}

func TestUserToken_refreshKeepsWhatTheRefreshDoesNotAnswer(t *testing.T) {
	c, _ := refreshServer(t, http.StatusOK)
	u := staleUser(c)
	u.Session.APIKey, u.Session.DeviceID, u.Session.Subject = "ak_kept", "dev-1", "0xabc"
	if got := u.Token(); got != "access-1" {
		t.Fatalf("Token() = %q, want access-1", got)
	}
	if u.Session.APIKey != "ak_kept" || u.Session.DeviceID != "dev-1" || u.Session.Subject != "0xabc" {
		t.Fatalf("a refresh dropped the session's API key %q, device %q or subject %q", u.Session.APIKey, u.Session.DeviceID, u.Session.Subject)
	}
	if u.Session.RefreshToken != "refresh-1" || u.Session.Stale(time.Now()) {
		t.Fatalf("the rotated refresh token %q was not kept, or the session is still stale", u.Session.RefreshToken)
	}
}

func TestUserToken_failedRefreshIsNotResentOnEveryCall(t *testing.T) {
	c, calls := refreshServer(t, http.StatusUnauthorized)
	u := staleUser(c)
	for range 5 {
		u.Token()
	}
	if calls.Load() != 1 {
		t.Fatalf("%d refresh attempts in a row after a refusal, want 1 until refreshRetryAfter passes", calls.Load())
	}
	u.refreshFailedAt = time.Now().Add(-refreshRetryAfter)
	u.Token()
	if calls.Load() != 2 {
		t.Fatalf("%d refresh attempts after the retry window, want 2", calls.Load())
	}
}

func TestSessionStale_countsWallClock(t *testing.T) {
	issued := time.Now().Round(0).Add(-11 * time.Minute)
	s := &Session{RefreshToken: "r", ExpiresIn: 900, issuedAt: issued}
	if !s.Stale(time.Now()) {
		t.Fatal("a session issued 11 minutes ago by the wall clock is not stale")
	}
}
