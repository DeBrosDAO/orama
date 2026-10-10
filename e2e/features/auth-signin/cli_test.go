//go:build e2e_fleet

package authsignin

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/e2e/harness"
	"github.com/DeBrosOfficial/network/e2e/harness/oramacli"
)

// Where and how the CLI keeps its session (core/pkg/auth/credentials.go).
const (
	credentialsFile = "credentials.json"
	credentialsPerm = 0o600
	// exitAuth is clierr's exit code for "sign in again" (cmd/orama/internal/clierr).
	exitAuth = 3
)

// storedCred is one credential in ~/.orama/credentials.json (v2.0 store).
type storedCred struct {
	APIKey               string    `json:"api_key"`
	RefreshToken         string    `json:"refresh_token"`
	AccessToken          string    `json:"access_token"`
	AccessTokenExpiresAt time.Time `json:"access_token_expires_at"`
	Namespace            string    `json:"namespace"`
	Wallet               string    `json:"wallet"`
}

func credPath(cli *oramacli.Runner) string {
	return filepath.Join(cli.Home, oramacli.ConfigDirName, credentialsFile)
}

// readCreds returns the default credential stored for gatewayURL.
func readCreds(t testing.TB, cli *oramacli.Runner, gatewayURL string) *storedCred {
	t.Helper()
	store := readStore(t, cli)
	gw, _ := store["gateways"].(map[string]any)[gatewayURL].(map[string]any)
	list, _ := gw["credentials"].([]any)
	if len(list) == 0 {
		return nil
	}
	idx, _ := gw["default_index"].(float64)
	raw, err := json.Marshal(list[int(idx)])
	if err != nil {
		t.Fatal(err)
	}
	var c storedCred
	if err := json.Unmarshal(raw, &c); err != nil {
		t.Fatal(err)
	}
	return &c
}

func readStore(t testing.TB, cli *oramacli.Runner) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(credPath(cli))
	if err != nil {
		t.Fatalf("failed to read the CLI's credential file: %v", err)
	}
	var store map[string]any
	if err := json.Unmarshal(raw, &store); err != nil {
		t.Fatalf("credential file is not JSON: %v", err)
	}
	if store["gateways"] == nil {
		store["gateways"] = map[string]any{}
	}
	return store
}

// loggedIn is an isolated HOME signed in to the lobby as the run's operator
// wallet (no terminal, so the CLI does not ask for a namespace).
func loggedIn(t testing.TB) *oramacli.Runner {
	t.Helper()
	cli := harness.CLI(t).Isolated(t)
	cli.MustOK(t, "auth", "login")
	return cli
}

// TestAuthLogin_storesASessionNotAKey: `orama auth login` keeps the access and
// refresh tokens, no API key in the lobby, in a 0600 file (docs/whitepaper/technical-reference/vol1/13-identity.md#the-command-line-client).
func TestAuthLogin_storesASessionNotAKey(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	cli := loggedIn(t)
	c := readCreds(t, cli, f.State.GatewayURL)
	if c == nil {
		t.Fatalf("no credential stored for %s", f.State.GatewayURL)
	}
	if c.AccessToken == "" || c.RefreshToken == "" || c.APIKey != "" || c.Namespace != lobby {
		t.Errorf("stored access set %v, refresh set %v, api key set %v, namespace %q",
			c.AccessToken != "", c.RefreshToken != "", c.APIKey != "", c.Namespace)
	}
	if !strings.EqualFold(c.Wallet, f.State.OperatorAddress) {
		t.Errorf("stored wallet %s, want the agent's %s", c.Wallet, f.State.OperatorAddress)
	}
	if d := time.Until(c.AccessTokenExpiresAt); d > accessTokenLifetime || d < accessTokenLifetime-lifetimeTolerance {
		t.Errorf("stored access token expires in %s, want about %s", d, accessTokenLifetime)
	}
	st, err := os.Stat(credPath(cli))
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm() != credentialsPerm {
		t.Errorf("credential file mode %o, want %o", st.Mode().Perm(), credentialsPerm)
	}
}

// TestAuthWhoami_asksTheGateway: whoami reports what the gateway says about
// the stored credential; status and list read only the local file.
func TestAuthWhoami_asksTheGateway(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	cli := loggedIn(t)
	out := cli.MustOK(t, "auth", "whoami").Stdout
	for _, want := range []string{"Authenticated with " + f.State.GatewayURL, "namespace:  " + lobby, "credential: jwt"} {
		if !strings.Contains(out, want) {
			t.Errorf("whoami output lacks %q:\n%s", want, out)
		}
	}
	if !containsFold(out, f.State.OperatorAddress) {
		t.Errorf("whoami does not name the operator wallet:\n%s", out)
	}
	for _, args := range [][]string{{"auth", "status"}, {"auth", "list"}} {
		if res := cli.MustOK(t, args...); !containsFold(res.Stdout, f.State.OperatorAddress) {
			t.Errorf("orama %s does not show the stored wallet:\n%s", strings.Join(args, " "), res.Stdout)
		}
	}
}

// TestAuthWhoami_withoutLoginIsAnAuthError: nothing stored is exit 3 with the
// next step named.
func TestAuthWhoami_withoutLoginIsAnAuthError(t *testing.T) {
	t.Parallel()
	cli := harness.CLI(t).Isolated(t)
	res, err := cli.Run(t.Context(), "auth", "whoami")
	if err != nil {
		t.Fatal(err)
	}
	if res.Exit != exitAuth || !strings.Contains(res.Stdout+res.Stderr, "orama auth login") {
		t.Fatalf("whoami with no session: want exit %d naming 'orama auth login', got %d\n%s%s", exitAuth, res.Exit, res.Stdout, res.Stderr)
	}
}

// TestAuthLogout_cliEndsTheSessionItHeld: `orama auth logout` ends the session
// on the gateway — the refresh token and the access token the machine held —
// and clears the file (docs/whitepaper/technical-reference/vol1/13-identity.md#revoking-a-device: "Logging out revokes the
// refresh token and the access token").
func TestAuthLogout_cliEndsTheSessionItHeld(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	cli := loggedIn(t)
	held := readCreds(t, cli, f.State.GatewayURL)
	cli.MustOK(t, "auth", "logout")
	if c := readCreds(t, cli, f.State.GatewayURL); c != nil {
		t.Errorf("a credential is still stored after logout")
	}
	c := harness.GW(t)
	expectRefreshRefused(t, refresh(t, c, held.RefreshToken, held.Namespace), "the logged-out refresh token")
	refusedEverywhere(t, perNode(t, f, c), held.AccessToken)
}

// TestAuth_groupListsSubcommands: `orama auth` alone lists what it can do.
func TestAuth_groupListsSubcommands(t *testing.T) {
	t.Parallel()
	out := harness.CLI(t).Isolated(t).MustOK(t, "auth").Stdout
	for _, sub := range []string{"approve", "list", "login", "logout", "sessions", "status", "switch", "whoami"} {
		if !strings.Contains(out, sub) {
			t.Errorf("`orama auth` does not list %q:\n%s", sub, out)
		}
	}
}

// TestAuthSwitch_nothingToSwitch: with no credential switch is an auth error;
// with exactly one it says there is nothing to switch to and changes nothing.
func TestAuthSwitch_nothingToSwitch(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	empty := harness.CLI(t).Isolated(t)
	if res, err := empty.Run(t.Context(), "auth", "switch"); err != nil || res.Exit != exitAuth {
		t.Errorf("switch with nothing stored: want exit %d, got %d %v", exitAuth, res.Exit, err)
	}
	cli := loggedIn(t)
	before := readCreds(t, cli, f.State.GatewayURL)
	out := cli.MustOK(t, "auth", "switch").Stdout
	if !strings.Contains(out, "Nothing to switch to") {
		t.Errorf("switch with one credential printed:\n%s", out)
	}
	if after := readCreds(t, cli, f.State.GatewayURL); after.RefreshToken != before.RefreshToken {
		t.Error("switching with one credential changed the stored session")
	}
}
