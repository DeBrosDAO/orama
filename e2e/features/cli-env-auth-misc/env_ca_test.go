//go:build e2e_fleet

package clienvauthmisc

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/harness"
)

// certRefusal is what Go's x509 verification says about the staging chain
// to a client that does not trust it ("certificate signed by unknown authority").
const certRefusal = "certificate"

// TestEnvAdd_withoutCAFileRefusesTheStagingChain: an environment added without
// --ca-file trusts the system roots alone (docs/whitepaper/technical-reference/appendices/d-cli-reference.md#orama-network-add:
// the CA is trusted only for the environment it was given to), so what its
// login does follows the CA the run's certificates come from. A fleet the run
// provisioned uses Let's Encrypt staging, which no system trust store accepts:
// the login must be refused with a certificate error and store no credential.
// The stagenet cluster serves Let's Encrypt production, which the system
// roots accept: the same login must succeed on the system roots alone.
// The run's own environment, which carries the CA for the same domain, is
// removed first so it cannot lend its trust.
func TestEnvAdd_withoutCAFileRefusesTheStagingChain(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	cli := isolated(t)
	cli.MustOK(t, "network", "remove", f.State.Env)
	name := e2eEnvPrefix + "noca"
	cli.MustOK(t, "network", "add", name, f.State.GatewayURL)
	cli.MustOK(t, "network", "use", name)
	res := run(t, cli, "auth", "login")
	who := run(t, cli, "auth", "whoami")
	if !f.State.StagingCerts() {
		if res.Exit != exitOK {
			t.Fatalf("auth login through an environment without a CA file, against production certificates the system roots accept: exit %d, want %d\n%s",
				res.Exit, exitOK, output(res))
		}
		if who.Exit != exitOK {
			t.Errorf("whoami after a login that succeeded on the system roots: exit %d, want %d\n%s", who.Exit, exitOK, output(who))
		}
		return
	}
	if res.Exit == exitOK || !strings.Contains(output(res), certRefusal) {
		t.Fatalf("auth login through an environment without the staging CA: exit %d, want a certificate refusal\n%s",
			res.Exit, output(res))
	}
	if who.Exit != exitAuth {
		t.Errorf("a refused login stored a credential: whoami exit %d\n%s", who.Exit, output(who))
	}
}

// TestEnvAdd_unusableCAFileRefused: --ca-file is checked when it is given, so
// a typo fails at `env add` rather than on the next command (environment.go
// SetEnvironmentCA), and no CA is recorded.
func TestEnvAdd_unusableCAFileRefused(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	dir := t.TempDir()
	garbage := filepath.Join(dir, "garbage.pem")
	empty := filepath.Join(dir, "empty.pem")
	for path, data := range map[string]string{garbage: "not a certificate\n", empty: ""} {
		if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for label, caFile := range map[string]string{
		"missing":   filepath.Join(dir, "absent.pem"),
		"garbage":   garbage,
		"empty":     empty,
		"directory": dir,
	} {
		t.Run(label, func(t *testing.T) {
			t.Parallel()
			cli := isolated(t)
			name := e2eEnvPrefix + "badca-" + label
			res := run(t, cli, "network", "add", name, f.State.GatewayURL, "--ca-file", caFile)
			if res.Exit == exitOK || !strings.Contains(output(res), caRefused) {
				t.Errorf("env add --ca-file %s: exit %d, want %q\n%s", label, res.Exit, caRefused, output(res))
			}
			if ca, _ := caFileOf(t, cli, name); ca != "" {
				t.Errorf("a refused CA file was recorded: %s", ca)
			}
		})
	}
}

// TestEnvCA_missingFileIsNamedError: a CA file that disappears after it was
// recorded makes every gateway command fail naming the environment and the
// fix, never a silent downgrade (website/src/docs/operator/node-setup.mdx "A missing CA file is
// an error naming the environment"). `orama network` and `orama version` still
// run, so the operator can repair it (core/cmd/orama/root.go needsEnvironmentCAs).
func TestEnvCA_missingFileIsNamedError(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	cli := isolated(t)
	name := e2eEnvPrefix + "lostca"
	ca := copyFile(t, f.State.CAFile, t.TempDir())
	cli.MustOK(t, "network", "add", name, f.State.GatewayURL, "--ca-file", ca)
	if err := os.Remove(ca); err != nil {
		t.Fatal(err)
	}
	res := run(t, cli, "auth", "status")
	if res.Exit != exitFailure || !strings.Contains(output(res), name) || !strings.Contains(output(res), "--ca-file") {
		t.Errorf("auth status with %s's CA file gone: exit %d, want %d naming %s and --ca-file\n%s",
			name, res.Exit, exitFailure, name, output(res))
	}
	cli.MustOK(t, "network", "list")
	cli.MustOK(t, "version")
	cli.MustOK(t, "network", "remove", name)
	cli.MustOK(t, "auth", "status")
}

// TestEnvAdd_unicodeNameRoundTrips: a name with right-to-left and combining
// characters is stored, selected, shown and removed as given; nothing else in
// the list changes.
func TestEnvAdd_unicodeNameRoundTrips(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	cli := isolated(t)
	name := e2eEnvPrefix + "\u202etsil-e\u0301"
	cli.MustOK(t, "network", "add", name, f.State.GatewayURL, "unicode \u05e9\u05dc\u05d5\u05dd")
	cli.MustOK(t, "network", "use", name)
	if cur := cli.MustOK(t, "network", "current").Stdout; !strings.Contains(cur, name) {
		t.Errorf("env current does not show %q:\n%s", name, cur)
	}
	cli.MustOK(t, "network", "remove", name)
	if _, ok := caFileOf(t, cli, name); ok {
		t.Errorf("%q still configured after env remove", name)
	}
	if _, ok := caFileOf(t, cli, f.State.Env); !ok {
		t.Errorf("removing %q removed the run's environment too", name)
	}
}
