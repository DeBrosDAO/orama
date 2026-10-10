//go:build e2e_fleet

package authdevices

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/e2e/harness"
	"github.com/DeBrosOfficial/network/e2e/harness/eventually"
	"github.com/DeBrosOfficial/network/e2e/harness/gw"
	"github.com/DeBrosOfficial/network/e2e/harness/ns"
	"github.com/DeBrosOfficial/network/e2e/harness/oramacli"
	"github.com/DeBrosOfficial/network/e2e/harness/wallet"
)

const (
	// exitUsage and exitAuth are clierr's exit codes (cmd/orama/internal/clierr).
	exitUsage = 2
	exitAuth  = 3
	keyPerm   = 0o600
)

// sessionRow is one row of `orama auth sessions`.
var sessionRow = regexp.MustCompile(`(?m)^\s+(\d+)\s+\S`)

func runCLI(t testing.TB, cli *oramacli.Runner, args ...string) oramacli.Result {
	t.Helper()
	res, err := cli.For(t).Run(t.Context(), args...)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

// TestAuthApprove_cliApprovesAWaitingLogin: `orama auth approve <code>` on a
// machine with a wallet signs the waiting login in as that wallet
// (docs/whitepaper/technical-reference/vol1/13-identity.md "Signing in from a machine with no wallet on it").
func TestAuthApprove_cliApprovesAWaitingLogin(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	c := harness.GW(t)
	d := startLogin(t, c, map[string]any{})
	out := harness.CLI(t).Isolated(t).MustOK(t, "auth", "approve", d.UserCode, "--namespace", gw.LobbyNamespace).Stdout
	if !strings.Contains(out, "Approved as") {
		t.Errorf("approve printed:\n%s", out)
	}
	var s gw.Session
	if err := poll(t, c, d.DeviceCode, nil).Expect(t, http.StatusOK).Decode(&s); err != nil {
		t.Fatal(err)
	}
	protect(t, c, s.AccessToken, s.RefreshToken)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		if _, err := c.Logout(ctx, s.AccessToken, s.RefreshToken, s.Namespace, false); err != nil {
			t.Errorf("cleanup: failed to end the approved session: %v", err)
		}
	})
	if !strings.EqualFold(s.Subject, f.State.OperatorAddress) || s.Namespace != gw.LobbyNamespace {
		t.Fatalf("the approved login is %s in %q, want the agent's wallet %s in the lobby", s.Subject, s.Namespace, f.State.OperatorAddress)
	}
}

// TestAuthApprove_cliDenyAndRefusals: --deny stops the waiting machine; a
// missing code or namespace is a usage error; a code nobody issued fails.
func TestAuthApprove_cliDenyAndRefusals(t *testing.T) {
	t.Parallel()
	c := harness.GW(t)
	cli := harness.CLI(t).Isolated(t)
	d := startLogin(t, c, map[string]any{})
	if out := cli.MustOK(t, "auth", "approve", d.UserCode, "--deny", "--namespace", gw.LobbyNamespace).Stdout; !strings.Contains(out, "Refused") {
		t.Errorf("deny printed:\n%s", out)
	}
	expectOAuthError(t, poll(t, c, d.DeviceCode, nil), "access_denied")
	if res := runCLI(t, cli, "auth", "approve", "", "--namespace", gw.LobbyNamespace); res.Exit != exitUsage {
		t.Errorf("approve with an empty code: want exit %d, got %d", exitUsage, res.Exit)
	}
	if res := runCLI(t, cli, "auth", "approve", "BCDF-GHJK"); res.Exit != exitUsage {
		t.Errorf("approve with no namespace and nothing stored: want exit %d, got %d", exitUsage, res.Exit)
	}
	if res := runCLI(t, cli, "auth", "approve", "BCDF-GHJK", "--namespace", gw.LobbyNamespace); res.Exit == 0 {
		t.Error("approving a code nobody issued succeeded")
	}
}

// TestAuthLogin_cliDeviceKeyEnrolsTheDevice: --device-key enrols the Ed25519
// key with the sign-in; the stored refresh token is device-bound (dv1_) and
// the device is listed active (docs/whitepaper/technical-reference/vol1/13-identity.md#signing-in-with-a-device).
func TestAuthLogin_cliDeviceKeyEnrolsTheDevice(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	n := ns.New(t, f, ns.Options{Via: ns.ViaOperator})
	cli := harness.CLI(t).Isolated(t)
	d := gw.NewDevice(t, wallet.AlgEd25519)
	priv, err := d.PrivateJWK()
	if err != nil {
		t.Fatal(err)
	}
	keyFile := filepath.Join(t.TempDir(), "device.jwk")
	if err := os.WriteFile(keyFile, priv, keyPerm); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{filepath.Join(t.TempDir(), "missing.jwk"), writeTemp(t, `{"kty":"EC"}`), writeTemp(t, "not json")} {
		if res := runCLI(t, harness.CLI(t).Isolated(t), "auth", "login", "--namespace", n.Name, "--device-key", bad); res.Exit == 0 {
			t.Errorf("login with device key file %s succeeded", filepath.Base(bad))
		}
	}
	cli.MustOK(t, "auth", "login", "--namespace", n.Name, "--device-key", keyFile)
	access, refresh := storedTokens(t, cli, f.State.GatewayURL)
	if !strings.HasPrefix(refresh, deviceRefreshPrefix) {
		t.Errorf("the stored refresh token is not device-bound (%s)", deviceRefreshPrefix)
	}
	devices, _, err := harness.GW(t).For(t).Devices(t.Context(), access)
	if err != nil || len(devices) != 1 || devices[0].ID != d.ID() || !devices[0].Current {
		t.Fatalf("devices after --device-key login: %+v %v", devices, err)
	}
}

// TestAuthSessions_cliListAndRevoke: `orama auth sessions` lists the wallet's
// sessions in the namespace and `sessions revoke <id>` ends one, whose
// machine is refused within ten seconds. (--all is not run: it revokes the
// run operator's wallet everywhere, which every other test signs in as.)
func TestAuthSessions_cliListAndRevoke(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	n := ns.New(t, f, ns.Options{Via: ns.ViaOperator})
	before := listedSessionIDs(t, n.CLI)
	other := harness.CLI(t).Isolated(t)
	other.MustOK(t, "auth", "login", "--namespace", n.Name)
	var added []string
	for id := range listedSessionIDs(t, n.CLI) {
		if !before[id] {
			added = append(added, id)
		}
	}
	if len(added) != 1 {
		t.Fatalf("a second machine's login added %d listed sessions (%v), want 1", len(added), added)
	}
	if out := n.CLI.MustOK(t, "auth", "sessions", "revoke", added[0]).Stdout; !strings.Contains(out, "Session "+added[0]+" ended") {
		t.Errorf("revoke printed:\n%s", out)
	}
	eventually.Require(t, pollEvery, revocationStaleness+stalenessSlack, "the ended machine to be refused", func() (bool, error) {
		res := runCLI(t, other, "auth", "whoami")
		if res.Exit == exitAuth {
			return true, nil
		}
		return false, fmt.Errorf("whoami exit %d", res.Exit)
	})
	n.CLI.MustOK(t, "auth", "whoami")
	for _, args := range [][]string{{"revoke"}, {"revoke", "0"}, {"revoke", "abc"}, {"revoke", "999999999"}} {
		if res := runCLI(t, n.CLI, append([]string{"auth", "sessions"}, args...)...); res.Exit == 0 {
			t.Errorf("auth sessions %v succeeded", args)
		}
	}
}

func listedSessionIDs(t testing.TB, cli *oramacli.Runner) map[string]bool {
	t.Helper()
	out := cli.MustOK(t, "auth", "sessions").Stdout
	ids := map[string]bool{}
	for _, m := range sessionRow.FindAllStringSubmatch(out, -1) {
		ids[m[1]] = true
	}
	if len(ids) == 0 {
		t.Fatalf("`orama auth sessions` listed nothing for a signed-in machine:\n%s", out)
	}
	return ids
}

// storedTokens reads the default credential's tokens for gatewayURL and
// registers them for redaction.
func storedTokens(t testing.TB, cli *oramacli.Runner, gatewayURL string) (access, refresh string) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(cli.Home, oramacli.ConfigDirName, "credentials.json"))
	if err != nil {
		t.Fatal(err)
	}
	var store struct {
		Gateways map[string]struct {
			Credentials []struct {
				AccessToken  string `json:"access_token"`
				RefreshToken string `json:"refresh_token"`
			} `json:"credentials"`
			DefaultIndex int `json:"default_index"`
		} `json:"gateways"`
	}
	if err := json.Unmarshal(raw, &store); err != nil {
		t.Fatal(err)
	}
	g, ok := store.Gateways[gatewayURL]
	if !ok || g.DefaultIndex >= len(g.Credentials) {
		t.Fatalf("no credential stored for %s", gatewayURL)
	}
	cred := g.Credentials[g.DefaultIndex]
	protect(t, harness.GW(t), cred.AccessToken, cred.RefreshToken)
	return cred.AccessToken, cred.RefreshToken
}

func writeTemp(t testing.TB, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "key.jwk")
	if err := os.WriteFile(p, []byte(content), keyPerm); err != nil {
		t.Fatal(err)
	}
	return p
}
