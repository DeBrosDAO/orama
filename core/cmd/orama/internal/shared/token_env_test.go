package shared

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/cmd/orama/internal/clierr"
)

// A key the gateway refuses is an authentication failure, not the generic one,
// and the message names ORAMA_TOKEN so it is not mistaken for a missing login.
func TestAuthToken_refusedEnvTokenIsTheAuthExit(t *testing.T) {
	home := isolatedHome(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprint(w, `{"code":"AUTH_INVALID_KEY","error":"invalid key"}`)
	}))
	defer server.Close()
	writeActiveEnvironment(t, home, "devnet", server.URL)
	const garbage = "e2e-garbage-token-7f3c"
	t.Setenv(TokenEnvVar, garbage)

	_, err := GetAuthToken()
	if got := clierr.CodeOf(err); got != clierr.CodeAuth {
		t.Fatalf("exit code %d (%v), want %d", got, err, clierr.CodeAuth)
	}
	if !strings.Contains(err.Error(), TokenEnvVar) {
		t.Errorf("the refusal does not name %s: %v", TokenEnvVar, err)
	}
	if strings.Contains(err.Error(), garbage) {
		t.Errorf("the refusal echoes the token: %v", err)
	}
}

// A gateway that cannot be reached is not a refusal of the token.
func TestAuthToken_unreachableGatewayIsNotTheAuthExit(t *testing.T) {
	home := isolatedHome(t)
	writeActiveEnvironment(t, home, "devnet", "http://127.0.0.1:1")
	t.Setenv(TokenEnvVar, "ak_ci_token:default")

	_, err := GetAuthToken()
	if err == nil {
		t.Fatal("no error for an unreachable gateway")
	}
	if got := clierr.CodeOf(err); got == clierr.CodeAuth {
		t.Fatalf("an unreachable gateway exited with the auth code: %v", err)
	}
}

func TestBearerNamespace(t *testing.T) {
	for name, c := range map[string]struct {
		status  int
		body    string
		want    string
		wantErr int
	}{
		"the token's namespace": {http.StatusOK, `{"authenticated":true,"namespace":"acme"}`, "acme", clierr.CodeOK},
		"refused":               {http.StatusUnauthorized, `{"error":"no"}`, "", clierr.CodeAuth},
		"not authenticated":     {http.StatusOK, `{"authenticated":false}`, "", clierr.CodeAuth},
		"unreadable":            {http.StatusOK, `<html>`, "", clierr.CodeFailure},
		"gateway error":         {http.StatusBadGateway, `bad gateway`, "", clierr.CodeFailure},
	} {
		t.Run(name, func(t *testing.T) {
			var sawBearer string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				sawBearer = r.Header.Get("Authorization")
				w.WriteHeader(c.status)
				fmt.Fprint(w, c.body)
			}))
			defer server.Close()
			got, err := BearerNamespace(server.URL, "tok")
			if code := clierr.CodeOf(err); code != c.wantErr {
				t.Fatalf("exit code %d (%v), want %d", code, err, c.wantErr)
			}
			if got != c.want {
				t.Errorf("namespace %q, want %q", got, c.want)
			}
			if sawBearer != "Bearer tok" {
				t.Errorf("the gateway was sent %q", sawBearer)
			}
		})
	}
	if _, err := BearerNamespace("http://127.0.0.1:1", "tok"); clierr.CodeOf(err) != clierr.CodeUnavailable {
		t.Errorf("an unreachable gateway exited %d, want %d", clierr.CodeOf(err), clierr.CodeUnavailable)
	}
}
