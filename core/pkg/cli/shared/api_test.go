package shared

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/auth"
)

// The gateway a request goes to and the credential attached to it must come
// from one decision. These tests pin the precedence and, most importantly, the
// cross-gateway case: pointing the CLI at one gateway must never send it the
// key stored for another.

// isolatedHome points HOME at a scratch directory and clears every gateway
// environment variable, so a test starts from a shell with nothing configured.
func isolatedHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	for _, name := range []string{"ORAMA_API_URL", "ORAMA_GATEWAY_URL", "ORAMA_GATEWAY", TokenEnvVar} {
		t.Setenv(name, "")
	}
	return home
}

// writeActiveEnvironment writes an environments.json whose active entry points
// at gatewayURL.
func writeActiveEnvironment(t *testing.T, home, name, gatewayURL string) {
	t.Helper()
	dir := filepath.Join(home, ".orama")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	cfg := map[string]any{
		"environments":       []map[string]string{{"name": name, "gateway_url": gatewayURL}},
		"active_environment": name,
	}
	data, _ := json.MarshalIndent(cfg, "", "  ")
	if err := os.WriteFile(filepath.Join(dir, "environments.json"), data, 0600); err != nil {
		t.Fatalf("write environments.json: %v", err)
	}
}

// storeCredential saves an API key for exactly one gateway.
func storeCredential(t *testing.T, home, gatewayURL, apiKey string) {
	t.Helper()
	dir := filepath.Join(home, ".orama")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	store := map[string]any{
		"version": "2.0",
		"gateways": map[string]any{
			gatewayURL: map[string]any{
				"credentials":     []map[string]any{{"api_key": apiKey, "namespace": "default"}},
				"default_index":   0,
				"last_used_index": 0,
			},
		},
	}
	data, _ := json.MarshalIndent(store, "", "  ")
	if err := os.WriteFile(filepath.Join(dir, "credentials.json"), data, 0600); err != nil {
		t.Fatalf("write credentials.json: %v", err)
	}
}

func TestGatewayURL_Precedence(t *testing.T) {
	t.Run("explicit override beats everything", func(t *testing.T) {
		home := isolatedHome(t)
		writeActiveEnvironment(t, home, "devnet", "https://from-config")
		t.Setenv("ORAMA_API_URL", "https://from-env")

		got, err := GatewayURL("https://from-flag")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != "https://from-flag" {
			t.Fatalf("got %q, want the explicit override", got)
		}
	})

	t.Run("ORAMA_API_URL beats the active environment", func(t *testing.T) {
		home := isolatedHome(t)
		writeActiveEnvironment(t, home, "devnet", "https://from-config")
		t.Setenv("ORAMA_API_URL", "https://from-env")

		got, err := GetAPIURL()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != "https://from-env" {
			t.Fatalf("got %q, want the environment variable", got)
		}
	})

	t.Run("legacy gateway variables are still honoured, in order", func(t *testing.T) {
		isolatedHome(t)
		t.Setenv("ORAMA_GATEWAY", "https://legacy-gateway")

		got, err := GetAPIURL()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != "https://legacy-gateway" {
			t.Fatalf("got %q, want ORAMA_GATEWAY", got)
		}

		t.Setenv("ORAMA_GATEWAY_URL", "https://legacy-gateway-url")
		got, err = GetAPIURL()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != "https://legacy-gateway-url" {
			t.Fatalf("got %q, want ORAMA_GATEWAY_URL to outrank ORAMA_GATEWAY", got)
		}
	})

	t.Run("falls through to the active environment", func(t *testing.T) {
		home := isolatedHome(t)
		writeActiveEnvironment(t, home, "testnet", "https://from-config")

		got, err := GetAPIURL()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != "https://from-config" {
			t.Fatalf("got %q, want the active environment", got)
		}
	})

	// An unconfigured shell used to silently talk to a hardcoded live network.
	t.Run("an unconfigured shell is an error, not a default network", func(t *testing.T) {
		isolatedHome(t)

		got, err := GetAPIURL()
		if !errors.Is(err, auth.ErrNoGateway) {
			t.Fatalf("expected ErrNoGateway, got %q / %v", got, err)
		}
	})
}

// exchangeGateway is a gateway that answers the one request the CLI makes with
// a key: trading it for a session. It records what it was given.
func exchangeGateway(t *testing.T, token string) (url string, presented *string) {
	t.Helper()
	seen := ""
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/auth/token" {
			t.Errorf("the CLI called %s; the only request it should make with a key is the exchange", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		seen = strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"access_token":%q,"token_type":"Bearer","expires_in":900}`, token)
	}))
	t.Cleanup(server.Close)
	return server.URL, &seen
}

// The defect this change exists for.
func TestAuthToken_NeverSendsAnotherGatewaysKey(t *testing.T) {
	home := isolatedHome(t)
	gatewayA, presented := exchangeGateway(t, "token-for-a")
	writeActiveEnvironment(t, home, "devnet", gatewayA)
	storeCredential(t, home, gatewayA, "ak_gateway_a_key:default")

	// Sanity: against gateway A the stored key buys a session, and the key
	// itself goes only to the exchange.
	token, err := GetAuthToken()
	if err != nil || token != "token-for-a" {
		t.Fatalf("expected gateway A's session, got %q / %v", token, err)
	}
	if *presented != "ak_gateway_a_key:default" {
		t.Fatalf("the exchange was given %q, want gateway A's key", *presented)
	}

	// Now point the CLI at a different gateway. There is no credential for it,
	// so the call must fail rather than attach gateway A's key.
	t.Setenv("ORAMA_API_URL", "https://gateway-b")

	token, err = GetAuthToken()
	if err == nil {
		t.Fatalf("expected an error for a gateway with no credential, got token %q", token)
	}
	if token == "ak_gateway_a_key:default" || token == "token-for-a" {
		t.Fatal("sent gateway A's credential to gateway B")
	}
	if !strings.Contains(err.Error(), "https://gateway-b") {
		t.Fatalf("the error should name the gateway actually being called, got: %v", err)
	}
}

func TestAuthToken_UsesTheCredentialForTheOverriddenGateway(t *testing.T) {
	home := isolatedHome(t)
	gatewayB, presented := exchangeGateway(t, "token-for-b")
	writeActiveEnvironment(t, home, "devnet", "https://gateway-a")
	storeCredential(t, home, "https://gateway-a", "ak_gateway_a_key:default")
	storeCredentialAppend(t, home, gatewayB, "ak_gateway_b_key:default")

	t.Setenv("ORAMA_API_URL", gatewayB)

	token, err := GetAuthToken()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if token != "token-for-b" {
		t.Fatalf("got %q, want a session from gateway B", token)
	}
	if *presented != "ak_gateway_b_key:default" {
		t.Fatalf("the exchange was given %q, want gateway B's own key", *presented)
	}
}

// A key in the environment is a CI credential. It is exchanged once per run
// rather than sent on every request the run makes.
func TestAuthToken_EnvKeyIsExchangedForASession(t *testing.T) {
	home := isolatedHome(t)
	gateway, presented := exchangeGateway(t, "token-for-ci")
	writeActiveEnvironment(t, home, "devnet", gateway)
	t.Setenv(TokenEnvVar, "ak_ci_token:default")

	token, err := GetAuthToken()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if token != "token-for-ci" {
		t.Fatalf("got %q, want the exchanged session", token)
	}
	if *presented != "ak_ci_token:default" {
		t.Fatalf("the exchange was given %q, want the key from the environment", *presented)
	}
}

// ORAMA_TOKEN takes either. A token is already the thing to send, and
// exchanging it would be a round trip to be told it is not a key.
func TestAuthToken_EnvTokenIsSentAsItIs(t *testing.T) {
	home := isolatedHome(t)
	writeActiveEnvironment(t, home, "devnet", "https://gateway-a")
	const jwt = "eyJhbGciOiJFZERTQSJ9.eyJzdWIiOiIweG93bmVyIn0.c2ln"
	t.Setenv(TokenEnvVar, jwt)

	// No server is stood up: a request would fail, which is the assertion.
	token, err := GetAuthToken()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if token != jwt {
		t.Fatalf("got %q, want the token from the environment unchanged", token)
	}
}

// storeCredentialAppend adds a second gateway to an existing credentials file.
func storeCredentialAppend(t *testing.T, home, gatewayURL, apiKey string) {
	t.Helper()
	path := filepath.Join(home, ".orama", "credentials.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read credentials.json: %v", err)
	}
	var store map[string]any
	if err := json.Unmarshal(data, &store); err != nil {
		t.Fatalf("parse credentials.json: %v", err)
	}
	gateways, _ := store["gateways"].(map[string]any)
	gateways[gatewayURL] = map[string]any{
		"credentials":     []map[string]any{{"api_key": apiKey, "namespace": "default"}},
		"default_index":   0,
		"last_used_index": 0,
	}
	out, _ := json.MarshalIndent(store, "", "  ")
	if err := os.WriteFile(path, out, 0600); err != nil {
		t.Fatalf("write credentials.json: %v", err)
	}
}
