package auth

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// A refresh token rotates on use, so two renewals of one stored session must
// not both present it: the second is a replay, and the gateway refuses it.

// rotatingGateway behaves like /v1/auth/refresh: it accepts only the current
// refresh token, rotates it, and refuses any other as a replay.
type rotatingGateway struct {
	*httptest.Server
	mu        sync.Mutex
	current   string
	refreshes int
	replays   int
}

func newRotatingGateway(t *testing.T, current string) *rotatingGateway {
	t.Helper()
	g := &rotatingGateway{current: current}
	g.Server = httptest.NewServer(http.HandlerFunc(g.refresh))
	t.Cleanup(g.Close)
	return g
}

// counts returns how many refreshes the gateway answered, and how many of them
// were replays.
func (g *rotatingGateway) counts() (refreshes, replays int) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.refreshes, g.replays
}

func (g *rotatingGateway) refresh(w http.ResponseWriter, r *http.Request) {
	var body map[string]string
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.refreshes++
	if body["refresh_token"] != g.current {
		g.replays++
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprint(w, `{"error":"invalid or expired refresh token"}`)
		return
	}
	g.current = fmt.Sprintf("refresh-%d", g.refreshes)
	fmt.Fprintf(w, `{"access_token":"access-%d","refresh_token":%q,"expires_in":900}`, g.refreshes, g.current)
}

// storeSession writes creds to the credential file under $HOME and returns the
// store and credential a command would load.
func storeSession(t *testing.T, gatewayURL string, creds *Credentials) (*EnhancedCredentialStore, *Credentials) {
	t.Helper()
	store := &EnhancedCredentialStore{Version: "2.0"}
	store.AddCredential(gatewayURL, creds)
	if err := store.Save(); err != nil {
		t.Fatalf("save the credential file: %v", err)
	}
	return loadSession(t, gatewayURL)
}

func loadSession(t *testing.T, gatewayURL string) (*EnhancedCredentialStore, *Credentials) {
	t.Helper()
	store, err := LoadEnhancedCredentials()
	if err != nil {
		t.Fatalf("load the credential file: %v", err)
	}
	creds := store.GetDefaultCredential(gatewayURL)
	if creds == nil {
		t.Fatalf("no stored credential for %s", gatewayURL)
	}
	return store, creds
}

func reloadSession(t *testing.T, gatewayURL string) *Credentials {
	t.Helper()
	_, creds := loadSession(t, gatewayURL)
	return creds
}

// Every caller loads the file before asking — as noderesolver.LoadBearer does
// for each of the monitor's renewals — so they all hold the same, soon to be
// retired, refresh token.
func TestBearer_concurrentRenewalsRefreshOnce(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	gateway := newRotatingGateway(t, "refresh-0")
	storeSession(t, gateway.URL, expiredSession("refresh-0"))

	const callers = 8
	loaded := make([]*Credentials, callers)
	stores := make([]*EnhancedCredentialStore, callers)
	for i := range callers {
		stores[i], loaded[i] = loadSession(t, gateway.URL)
	}

	tokens := make([]string, callers)
	errs := make([]error, callers)
	var wg sync.WaitGroup
	for i := range callers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			tokens[i], errs[i] = Bearer(gateway.URL, stores[i], loaded[i])
		}()
	}
	wg.Wait()

	for i := range callers {
		if errs[i] != nil {
			t.Errorf("caller %d: %v", i, errs[i])
		} else if tokens[i] != "access-1" {
			t.Errorf("caller %d got %q, want the one renewal's token", i, tokens[i])
		}
	}
	if refreshes, replays := gateway.counts(); refreshes != 1 || replays != 0 {
		t.Errorf("%d refreshes (%d replays), want exactly one", refreshes, replays)
	}
	if got := reloadSession(t, gateway.URL); got.RefreshToken != "refresh-1" {
		t.Errorf("stored refresh token = %q, want the rotated one", got.RefreshToken)
	}
}

// Another process renewed the session after this one loaded the file. The
// stale copy's refresh token is already retired; the renewal on disk is used.
func TestBearer_usesASessionAnotherCallerAlreadyRenewed(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	gateway := newRotatingGateway(t, "refresh-1")
	store, stale := storeSession(t, gateway.URL, expiredSession("refresh-0"))

	renewed := expiredSession("refresh-1")
	renewed.AccessToken = "renewed-elsewhere"
	renewed.AccessTokenExpiresAt = time.Now().Add(10 * time.Minute)
	storeSession(t, gateway.URL, renewed)

	token, err := Bearer(gateway.URL, store, stale)
	if err != nil {
		t.Fatalf("Bearer: %v", err)
	}
	if token != "renewed-elsewhere" {
		t.Errorf("token = %q, want the one already on disk", token)
	}
	if refreshes, _ := gateway.counts(); refreshes != 0 {
		t.Errorf("%d refreshes, want none: the stored token was live", refreshes)
	}
	if stale.RefreshToken != "refresh-1" {
		t.Errorf("caller's refresh token = %q, want the stored one adopted", stale.RefreshToken)
	}
}

// A credential removed from disk while its renewal waited (a logout in another
// terminal) is neither renewed in memory — a long-running command would keep
// a signed-out session alive — nor written back, which would sign it in again.
func TestBearer_doesNotRestoreASignedOutCredential(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	gateway := newRotatingGateway(t, "refresh-0")
	store, creds := storeSession(t, gateway.URL, expiredSession("refresh-0"))

	emptied := &EnhancedCredentialStore{Version: "2.0"}
	if err := emptied.Save(); err != nil {
		t.Fatalf("save: %v", err)
	}

	if _, err := Bearer(gateway.URL, store, creds); err == nil || !strings.Contains(err.Error(), "signed out") {
		t.Fatalf("Bearer err = %v, want the signed-out credential refused", err)
	}
	if refreshes, _ := gateway.counts(); refreshes != 0 {
		t.Errorf("%d refreshes of a signed-out credential, want none", refreshes)
	}
	disk, err := LoadEnhancedCredentials()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if got := disk.GetDefaultCredential(gateway.URL); got != nil {
		t.Errorf("the signed-out credential was written back: %+v", got)
	}
}
