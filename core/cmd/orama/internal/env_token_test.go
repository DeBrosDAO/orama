package cli

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/DeBrosOfficial/network/cmd/orama/internal/clierr"
	"github.com/DeBrosOfficial/network/cmd/orama/internal/printer"
)

// envJWT is shaped like a token, so ORAMA_TOKEN is sent as it is.
const envJWT = "eyJhbGciOiJFZERTQSJ9.eyJzdWIiOiIweG93bmVyIn0.c2ln"

// tokenGateway answers the calls the namespace, members, audit and auth
// commands make, and records the bearer each one carried.
type tokenGateway struct {
	*httptest.Server
	mu      sync.Mutex
	bearers map[string]string
}

func newTokenGateway(t *testing.T) *tokenGateway {
	t.Helper()
	g := &tokenGateway{bearers: map[string]string{}}
	g.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		g.mu.Lock()
		g.bearers[r.URL.Path] = r.Header.Get("Authorization")
		g.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/auth/whoami":
			_, _ = w.Write([]byte(`{"authenticated":true,"namespace":"acme","principal":"0xowner","method":"jwt"}`))
		case "/v1/namespace/list":
			_, _ = w.Write([]byte(`{"namespaces":[{"name":"acme","cluster_status":"ready"},{"name":"other","cluster_status":"ready"}]}`))
		case "/v1/namespace/delete":
			_, _ = w.Write([]byte(`{"status":"deleted"}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(g.Close)
	return g
}

func (g *tokenGateway) bearerOn(path string) string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.bearers[path]
}

// envTokenHome is a HOME with no credential at all, pointed at gateway, with
// ORAMA_TOKEN set: the CI shape docs/AUTH.md describes.
func envTokenHome(t *testing.T, gateway string) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("ORAMA_API_URL", gateway)
	t.Setenv("ORAMA_GATEWAY_URL", "")
	t.Setenv("ORAMA_GATEWAY", "")
	t.Setenv("ORAMA_TOKEN", envJWT)
}

// Bug: namespace, members, audit and auth read only the stored session, so a
// CI job with ORAMA_TOKEN and no login was refused with "run 'orama auth
// login'".
func TestEnvToken_isTheCredentialOfEveryGatewayCommand(t *testing.T) {
	g := newTokenGateway(t)
	envTokenHome(t, g.URL)

	gatewayURL, token, err := loadAuthForNamespace()
	if err != nil {
		t.Fatalf("namespace, members and audit: %v", err)
	}
	if gatewayURL != g.URL || token != envJWT {
		t.Errorf("loadAuthForNamespace = %q, %q; want the gateway and ORAMA_TOKEN", gatewayURL, token)
	}
	bearer, err := currentBearer(g.URL)
	if err != nil || bearer != envJWT {
		t.Errorf("auth whoami and sessions: %q, %v; want ORAMA_TOKEN", bearer, err)
	}
}

func TestEnvToken_namespaceListAndDeleteSendItAndAskTheGatewayWhoItIs(t *testing.T) {
	g := newTokenGateway(t)
	envTokenHome(t, g.URL)

	var out bytes.Buffer
	if err := NamespaceList(printer.New(&out, &out)); err != nil {
		t.Fatalf("namespace list: %v", err)
	}
	if got := g.bearerOn("/v1/namespace/list"); got != "Bearer "+envJWT {
		t.Errorf("the list carried %q", got)
	}
	row := ""
	for _, line := range strings.Split(out.String(), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "acme") {
			row = line
		}
	}
	if !strings.Contains(row, "yes") {
		t.Errorf("the token's own namespace is not marked active:\n%s", out.String())
	}

	if err := NamespaceDelete(true); err != nil {
		t.Fatalf("namespace delete: %v", err)
	}
	if got := g.bearerOn("/v1/namespace/delete"); got != "Bearer "+envJWT {
		t.Errorf("the delete carried %q", got)
	}
}

func TestEnvToken_absentStillMeansLogin(t *testing.T) {
	g := newTokenGateway(t)
	envTokenHome(t, g.URL)
	t.Setenv("ORAMA_TOKEN", "")

	_, _, err := loadAuthForNamespace()
	if got := clierr.CodeOf(err); got != clierr.CodeAuth || !strings.Contains(err.Error(), "orama auth login") {
		t.Fatalf("no credential: exit %d, %v; want %d and the login hint", got, err, clierr.CodeAuth)
	}
}
