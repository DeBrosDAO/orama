//go:build e2e_fleet

package clienvauthmisc

import (
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/harness"
)

// tokenEnvVar is the CI credential (docs/AUTH.md, docs/DEPLOYMENT_GUIDE.md "In CI").
const tokenEnvVar = "ORAMA_TOKEN"

// garbageToken is a credential no gateway issued, recognisable if echoed.
const garbageToken = "e2e-garbage-token-7f3c"

// credentialCommands are read-only commands that need a signed-in wallet.
// Each is refused by a HOME with no credential before anything reaches a
// gateway authenticated.
func credentialCommands(env string) [][]string {
	return [][]string{
		{"auth", "whoami"},
		{"auth", "sessions"},
		{"namespace", "list"},
		{"namespace", "keys", "list"},
		{"members", "list"},
		{"audit"},
		{"app", "list"},
		{"db", "list"},
		{"function", "list"},
		{"domain", "list"},
		{"cluster", "settings", "show"},
		{"operator", "list"},
		{"monitor", "alerts", "--env", env},
	}
}

// TestCredentials_missingIsAuthError: with no stored credential every command
// that needs one exits with the auth code (3) and says to run `orama auth
// login` (core/cmd/orama/internal/clierr CodeAuth: "a missing, expired or
// insufficient credential ... `orama auth login` is the fix").
func TestCredentials_missingIsAuthError(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	cli := isolated(t)
	for _, args := range credentialCommands(f.State.Env) {
		t.Run(strings.Join(args[:min(len(args), 3)], "_"), func(t *testing.T) {
			t.Parallel()
			res := run(t, cli.For(t), args...)
			if res.Exit != exitAuth || !strings.Contains(output(res), loginHint) {
				t.Errorf("orama %v with no credential: exit %d, want %d and %q\n%s",
					args, res.Exit, exitAuth, loginHint, output(res))
			}
		})
	}
}

// TestCredentials_garbageEnvTokenRefused: an ORAMA_TOKEN no gateway issued is
// refused as an auth failure and never printed back.
func TestCredentials_garbageEnvTokenRefused(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	cli := isolated(t)
	cli.Env = append(cli.Env, tokenEnvVar+"="+garbageToken)
	for _, args := range credentialCommands(f.State.Env)[2:6] {
		res := run(t, cli, args...)
		if res.Exit == exitOK {
			t.Errorf("orama %v accepted a garbage %s:\n%s", args, tokenEnvVar, output(res))
		}
		if res.Exit != exitAuth {
			t.Errorf("orama %v with a garbage %s: exit %d, want %d\n%s", args, tokenEnvVar, res.Exit, exitAuth, output(res))
		}
		expectNoEcho(t, res, garbageToken)
	}
}

// TestCredentials_hostileEnvTokenRefused: a token carrying header-injection
// and control bytes must be refused, not smuggled into a request.
func TestCredentials_hostileEnvTokenRefused(t *testing.T) {
	t.Parallel()
	cli := isolated(t)
	hostile := "x\r\nX-Orama-Injected: 1"
	cli.Env = append(cli.Env, tokenEnvVar+"="+hostile)
	res := run(t, cli, "namespace", "list")
	if res.Exit == exitOK {
		t.Fatalf("namespace list accepted a token with CR/LF:\n%s", output(res))
	}
	if strings.Contains(output(res), "X-Orama-Injected") {
		t.Errorf("the CLI printed the hostile token back:\n%s", output(res))
	}
}

// TestCredentials_operatorSessionWorks is the positive control of the matrix:
// the run's signed-in operator HOME gets past authentication on the same
// read-only commands.
func TestCredentials_operatorSessionWorks(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	cli := harness.CLI(t)
	for _, args := range [][]string{
		{"auth", "whoami"},
		{"cluster", "settings", "show"},
		{"operator", "list"},
		{"monitor", "alerts", "--env", f.State.Env},
	} {
		cli.MustOK(t, args...)
	}
}
