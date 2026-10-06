package auth

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// What the CLI sends, and what it has to do to get it.
//
// The order is not a fallback chain: each step is what to do when the one
// before it has nothing to offer. Only the last of them sends a long-lived
// credential anywhere.

// recordingGateway answers the refresh and exchange endpoints and remembers
// every request it was sent.
type recordingGateway struct {
	*httptest.Server
	refreshCalls  int
	exchangeCalls int
	presentedKeys []string
	refuseRefresh bool
}

func newRecordingGateway(t *testing.T) *recordingGateway {
	t.Helper()
	g := &recordingGateway{}
	g.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/auth/refresh":
			g.refreshCalls++
			if g.refuseRefresh {
				w.WriteHeader(http.StatusUnauthorized)
				fmt.Fprint(w, `{"error":"invalid or expired refresh token","code":"AUTH_EXPIRED"}`)
				return
			}
			var body map[string]string
			_ = json.NewDecoder(r.Body).Decode(&body)
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"access_token":"refreshed-%s","refresh_token":"next-refresh","expires_in":900}`,
				body["refresh_token"])
		case "/v1/auth/token":
			g.exchangeCalls++
			g.presentedKeys = append(g.presentedKeys, strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"access_token":"exchanged","expires_in":900}`)
		default:
			t.Errorf("unexpected request to %s: renewing a session should touch nothing else", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(g.Close)
	return g
}

func TestBearer_usesALiveAccessTokenWithoutAskingAnybody(t *testing.T) {
	gateway := newRecordingGateway(t)
	creds := &Credentials{
		AccessToken:          "still-good",
		AccessTokenExpiresAt: time.Now().Add(10 * time.Minute),
		RefreshToken:         "refresh",
		APIKey:               "orama_sk_key_x",
	}

	token, err := Bearer(gateway.URL, nil, creds)
	if err != nil {
		t.Fatalf("Bearer: %v", err)
	}
	if token != "still-good" {
		t.Errorf("token = %q, want the stored one", token)
	}
	if gateway.refreshCalls != 0 || gateway.exchangeCalls != 0 {
		t.Errorf("a live token cost %d refreshes and %d exchanges", gateway.refreshCalls, gateway.exchangeCalls)
	}
}

// A token that expires while the request it was attached to is in flight is
// worse than one that was replaced a minute early.
func TestBearer_replacesATokenInsideTheRenewalMargin(t *testing.T) {
	gateway := newRecordingGateway(t)
	creds := &Credentials{
		AccessToken:          "about-to-expire",
		AccessTokenExpiresAt: time.Now().Add(accessTokenRenewalMargin / 2),
		RefreshToken:         "refresh",
	}

	token, err := Bearer(gateway.URL, nil, creds)
	if err != nil {
		t.Fatalf("Bearer: %v", err)
	}
	if token == "about-to-expire" {
		t.Error("a token inside the renewal margin was sent anyway")
	}
	if gateway.refreshCalls != 1 {
		t.Errorf("%d refreshes, want 1", gateway.refreshCalls)
	}
}

func TestBearer_refreshesRatherThanSendingTheKey(t *testing.T) {
	gateway := newRecordingGateway(t)
	creds := &Credentials{RefreshToken: "the-refresh-token", APIKey: "orama_sk_key_x", Namespace: "anchat"}

	token, err := Bearer(gateway.URL, nil, creds)
	if err != nil {
		t.Fatalf("Bearer: %v", err)
	}
	if token != "refreshed-the-refresh-token" {
		t.Errorf("token = %q", token)
	}
	if gateway.exchangeCalls != 0 {
		t.Error("the key was sent even though there was a session to renew")
	}
	// The rotated refresh token is kept, or the next command presents one the
	// gateway has already retired and is told it is replaying a stolen one.
	if creds.RefreshToken != "next-refresh" {
		t.Errorf("refresh token = %q, want the rotated one", creds.RefreshToken)
	}
	if !creds.HasLiveAccessToken() {
		t.Error("the renewed session was not recorded")
	}
}

// A credential file written by an older CLI has a key and no session. It is
// exchanged once, not sent on every request.
func TestBearer_exchangesTheKeyWhenThereIsNoSession(t *testing.T) {
	gateway := newRecordingGateway(t)
	creds := &Credentials{APIKey: "orama_sk_key_x"}

	token, err := Bearer(gateway.URL, nil, creds)
	if err != nil {
		t.Fatalf("Bearer: %v", err)
	}
	if token != "exchanged" {
		t.Errorf("token = %q", token)
	}
	if gateway.exchangeCalls != 1 {
		t.Errorf("%d exchanges, want 1", gateway.exchangeCalls)
	}
	if len(gateway.presentedKeys) != 1 || gateway.presentedKeys[0] != "orama_sk_key_x" {
		t.Errorf("the exchange was given %v", gateway.presentedKeys)
	}
	if gateway.refreshCalls != 0 {
		t.Error("a refresh was attempted with no refresh token")
	}
}

// A refresh token the gateway refuses is spent, replayed or revoked. Keeping it
// makes every command from here on pay for one more failed round trip.
func TestBearer_forgetsARefreshTokenTheGatewayRefuses(t *testing.T) {
	gateway := newRecordingGateway(t)
	gateway.refuseRefresh = true
	creds := &Credentials{RefreshToken: "spent", APIKey: "orama_sk_key_x"}

	token, err := Bearer(gateway.URL, nil, creds)
	if err != nil {
		t.Fatalf("Bearer: %v", err)
	}
	if token != "exchanged" {
		t.Errorf("token = %q, want the key to have been exchanged", token)
	}
	if creds.RefreshToken != "" {
		t.Errorf("the refused refresh token was kept: %q", creds.RefreshToken)
	}

	// And the next call goes straight to the exchange.
	creds.AccessToken = ""
	creds.AccessTokenExpiresAt = time.Time{}
	if _, err := Bearer(gateway.URL, nil, creds); err != nil {
		t.Fatalf("second Bearer: %v", err)
	}
	if gateway.refreshCalls != 1 {
		t.Errorf("%d refreshes, want the refused one and no more", gateway.refreshCalls)
	}
}

// With no key to fall back to, a refused refresh is the end of the session and
// has to say so rather than reporting a network error.
func TestBearer_saysTheSessionHasEndedWhenThereIsNothingElse(t *testing.T) {
	gateway := newRecordingGateway(t)
	gateway.refuseRefresh = true
	creds := &Credentials{RefreshToken: "spent"}

	_, err := Bearer(gateway.URL, nil, creds)
	if err == nil {
		t.Fatal("a refused refresh with no key reported success")
	}
	if !strings.Contains(err.Error(), "orama auth login") {
		t.Errorf("the error does not say what to do: %v", err)
	}
}

func TestBearer_refusesACredentialItCannotUse(t *testing.T) {
	if _, err := Bearer("https://gateway", nil, nil); err == nil {
		t.Error("no credential at all produced a token")
	}
	if _, err := Bearer("https://gateway", nil, &Credentials{}); err == nil {
		t.Error("an empty credential produced a token")
	}
}

func TestLooksLikeJWT(t *testing.T) {
	for value, want := range map[string]bool{
		"eyJhbGciOiJFZERTQSJ9.eyJzdWIiOiIweCJ9.c2ln": true,
		"orama_sk_payload_check":                     false,
		"ak_legacy:default":                          false,
		"":                                           false,
		"ey.only.two.dots.too.many":                  false,
		"a.b.c":                                      false,
		"orama_sk.payload.check":                     false,
		"notatoken":                                  false,
	} {
		if got := LooksLikeJWT(value); got != want {
			t.Errorf("LooksLikeJWT(%q) = %v, want %v", value, got, want)
		}
	}
}

// BearerFromEnv takes either, and the difference is whether it costs a round
// trip.
func TestBearerFromEnv(t *testing.T) {
	gateway := newRecordingGateway(t)

	const jwt = "eyJhbGciOiJFZERTQSJ9.eyJzdWIiOiIweCJ9.c2ln"
	token, err := BearerFromEnv(gateway.URL, jwt)
	if err != nil {
		t.Fatalf("BearerFromEnv: %v", err)
	}
	if token != jwt {
		t.Errorf("a token was not sent as it is: %q", token)
	}
	if gateway.exchangeCalls != 0 {
		t.Error("a token was sent to the exchange endpoint")
	}

	token, err = BearerFromEnv(gateway.URL, "orama_sk_key_x")
	if err != nil {
		t.Fatalf("BearerFromEnv: %v", err)
	}
	if token != "exchanged" {
		t.Errorf("a key was not exchanged: %q", token)
	}

	if _, err := BearerFromEnv(gateway.URL, "   "); err == nil {
		t.Error("an empty ORAMA_TOKEN was accepted")
	}
}
