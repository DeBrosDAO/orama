package functions

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/DeBrosOfficial/network/cmd/orama/internal/clierr"
)

// gatewayAnswering is a gateway that answers every request with status, and a
// shell whose credential is ORAMA_TOKEN, shaped like a token so it is sent as is.
func gatewayAnswering(t *testing.T, status int) {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
		fmt.Fprint(w, `{"error":"nope"}`)
	}))
	t.Cleanup(server.Close)
	useGateway(t, server.URL)
}

func useGateway(t *testing.T, url string) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("ORAMA_API_URL", url)
	t.Setenv("ORAMA_TOKEN", "eyJhbGciOiJFZERTQSJ9.eyJzdWIiOiIweG93bmVyIn0.c2ln")
}

// Bug: every gateway refusal exited 1, so a script could not tell a function
// that is not there (4) from a credential the gateway refuses (3) or a gateway
// that cannot serve right now (5).
func TestFunctionCommands_gatewayAnswersMapToExitCodes(t *testing.T) {
	for status, want := range map[int]int{
		http.StatusNotFound:            clierr.CodeNotFound,
		http.StatusUnauthorized:        clierr.CodeAuth,
		http.StatusForbidden:           clierr.CodeAuth,
		http.StatusServiceUnavailable:  clierr.CodeUnavailable,
		http.StatusBadGateway:          clierr.CodeUnavailable,
		http.StatusGatewayTimeout:      clierr.CodeUnavailable,
		http.StatusInternalServerError: clierr.CodeFailure,
		http.StatusBadRequest:          clierr.CodeFailure,
	} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			gatewayAnswering(t, status)
			for name, run := range map[string]func() error{
				"get":      func() error { return runGet(GetCmd, []string{"absent-fn"}) },
				"versions": func() error { return runVersions(VersionsCmd, []string{"absent-fn"}) },
				"disable":  func() error { return runSetEnabled("absent-fn", false) },
				"invoke":   func() error { return runInvoke(InvokeCmd, []string{"absent-fn"}) },
			} {
				err := run()
				if got := clierr.CodeOf(err); got != want {
					t.Errorf("%s answered %d: exit code %d (%v), want %d", name, status, got, err, want)
				}
			}
		})
	}
}

func TestFunctionCommands_anUnreachableGatewayIsUnavailable(t *testing.T) {
	useGateway(t, "http://127.0.0.1:1")
	err := runGet(GetCmd, []string{"absent-fn"})
	if got := clierr.CodeOf(err); got != clierr.CodeUnavailable {
		t.Fatalf("exit code %d (%v), want %d", got, err, clierr.CodeUnavailable)
	}
}
