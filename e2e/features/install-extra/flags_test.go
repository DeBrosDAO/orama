//go:build e2e_fleet

package installextra

import (
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/features/internal/infra"
	"github.com/DeBrosOfficial/network/e2e/harness"
	"github.com/DeBrosOfficial/network/e2e/harness/fleet"
)

// The install refusals run on the joined extra, not on a core node: every
// case but the dry run is a real install should its refusal regress, and
// the extra is removed and wiped at the end of the test. They cannot run
// before setup: the node-local install needs the CLI setup puts there.

// nodeFingerprint is what an install run must not change on a live node:
// its config, overlay config, secrets, staged build, trust anchor and the
// supervisor's start time.
func nodeFingerprint(t testing.TB, f *fleet.Fleet, n fleet.Node) string {
	t.Helper()
	cmd := "sha256sum " + strings.Join([]string{infra.NodeConfigPath, infra.WireGuardConfPath, infra.StagedManifest,
		infra.ArchiveSigners}, " ") + " && ls -la --time-style=+%s " + infra.OramaSecretsDir +
		" && systemctl show -p ActiveEnterTimestampMonotonic --value " + infra.NodeUnit
	return f.MustExec(t, n, cmd).Stdout
}

// installDryRunChangesNothing: `orama node install --dry-run` on a member
// prints the plan under "DRY RUN - No changes will be made" and
// leaves the node exactly as it was (docs/CLI_REFERENCE.md "orama node install").
func installDryRunChangesNothing(t *testing.T, f *fleet.Fleet, extra harness.Extra) {
	n := extra.Node
	before := nodeFingerprint(t, f, n)
	out := infra.OnNode(t, f, n, "node", "install", "--dry-run", "--vps-ip", n.PublicIP, "--base-domain", f.State.BaseDomain)
	if out.Exit != infra.ExitOK || !strings.Contains(out.Stdout, "DRY RUN - No changes will be made") ||
		!strings.Contains(out.Stdout, n.PublicIP) {
		t.Fatalf("dry run: exit %d\n%s%s", out.Exit, out.Stdout, f.Redact(out.Stderr))
	}
	if after := nodeFingerprint(t, f, n); after != before {
		t.Fatalf("a dry run changed the node:\nbefore:\n%s\nafter:\n%s", before, after)
	}
}

// installFlagsRefused: every flag mistake is a
// usage error (exit 2) raised before the install touches the machine
// (core/cmd/orama/internal/production/install/flags.go): a genesis install
// without --operator-wallet, a wallet that is no address, an ACME directory
// that is not https, --expect-archive-signers without a join, and an address
// that is not a public IPv4.
func installFlagsRefused(t *testing.T, f *fleet.Fleet, extra harness.Extra) {
	n := extra.Node
	before := nodeFingerprint(t, f, n)
	bd := f.State.BaseDomain
	cases := []struct {
		args []string
		exit int
		why  string
	}{
		{[]string{"--vps-ip", n.PublicIP, "--base-domain", bd}, infra.ExitUsage, "needs --operator-wallet"},
		{[]string{"--vps-ip", n.PublicIP, "--base-domain", bd, "--operator-wallet", "0xnot-a-wallet"}, infra.ExitUsage, "--operator-wallet"},
		{[]string{"--vps-ip", n.PublicIP, "--base-domain", bd, "--operator-wallet", f.State.OperatorAddress, "--acme-ca", "http://acme.invalid/dir"}, infra.ExitUsage, "--acme-ca"},
		{[]string{"--vps-ip", n.PublicIP, "--base-domain", bd, "--operator-wallet", f.State.OperatorAddress, "--expect-archive-signers", f.State.OperatorAddress}, infra.ExitUsage, "for joining a cluster"},
		{[]string{"--dry-run", "--vps-ip", "10.0.0.5", "--base-domain", bd}, infra.ExitFailure, "--vps-ip"},
		{[]string{"--dry-run", "--vps-ip", "1.2.3.4.5", "--base-domain", bd}, infra.ExitFailure, "--vps-ip"},
		{[]string{"--dry-run", "--vps-ip", "‮1.2.3.4", "--base-domain", bd}, infra.ExitFailure, "--vps-ip"},
	}
	for _, c := range cases {
		out := infra.OnNode(t, f, n, append([]string{"node", "install"}, c.args...)...)
		if out.Exit != c.exit || !strings.Contains(out.Stdout+out.Stderr, c.why) {
			t.Errorf("install %v: exit %d, want %d naming %q:\n%s%s", c.args, out.Exit, c.exit, c.why, out.Stdout, f.Redact(out.Stderr))
		}
	}
	if after := nodeFingerprint(t, f, n); after != before {
		t.Fatalf("a refused install changed the node:\nbefore:\n%s\nafter:\n%s", before, after)
	}
}
