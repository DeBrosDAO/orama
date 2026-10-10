package shared

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/cmd/orama/internal/clierr"
)

// storeExpiredSession signs the stored credential for gatewayURL in with an
// access token that has already expired, so the next command must renew it.
func storeExpiredSession(t *testing.T, home, gatewayURL, refreshToken string) {
	t.Helper()
	store := map[string]any{"version": "2.0", "gateways": map[string]any{
		gatewayURL: map[string]any{"default_index": 0, "last_used_index": 0, "credentials": []map[string]any{{
			"namespace": "ns", "wallet": "0xabc", "access_token": "old", "refresh_token": refreshToken,
			"access_token_expires_at": time.Now().Add(-time.Hour).Format(time.RFC3339)}}}}}
	data, _ := json.Marshal(store)
	if err := os.WriteFile(filepath.Join(home, ".orama", "credentials.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
}

// A gateway refusing the refresh token ends the session, and a script reads
// that from the exit code (stagenet e2e, 2026-09-30: exit 1).
func TestAuthToken_refusedRefreshIsTheAuthExit(t *testing.T) {
	for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			home := isolatedHome(t)
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(status)
				fmt.Fprint(w, `{"error":"invalid or expired refresh token"}`)
			}))
			defer srv.Close()
			writeActiveEnvironment(t, home, "devnet", srv.URL)
			storeExpiredSession(t, home, srv.URL, "spent")

			_, err := GetAuthToken()
			if got := clierr.CodeOf(err); got != clierr.CodeAuth {
				t.Fatalf("exit code %d (%v), want %d", got, err, clierr.CodeAuth)
			}
			if !strings.Contains(err.Error(), "orama auth login") {
				t.Errorf("the refusal does not name the login: %v", err)
			}
		})
	}
}

// The same refusal with --namespace chooses a credential by namespace, and is
// the same exit.
func TestAuthTokenFor_refusedRefreshIsTheAuthExit(t *testing.T) {
	home := isolatedHome(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()
	writeActiveEnvironment(t, home, "devnet", srv.URL)
	storeExpiredSession(t, home, srv.URL, "spent")

	_, err := AuthTokenFor(srv.URL, "ns")
	if got := clierr.CodeOf(err); got != clierr.CodeAuth {
		t.Fatalf("exit code %d (%v), want %d", got, err, clierr.CodeAuth)
	}
}

// A refresh that failed for a reason that says nothing about the token leaves
// the session intact, so it is not the sign-in exit.
func TestAuthToken_transientRefreshFailureIsNotTheAuthExit(t *testing.T) {
	home := isolatedHome(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()
	writeActiveEnvironment(t, home, "devnet", srv.URL)
	storeExpiredSession(t, home, srv.URL, "good")

	_, err := GetAuthToken()
	if err == nil || clierr.CodeOf(err) == clierr.CodeAuth {
		t.Fatalf("a 503 on refresh: %v, want an error that is not the auth exit", err)
	}
}
