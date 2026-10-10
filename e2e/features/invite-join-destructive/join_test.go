//go:build e2e_fleet

package invitejoindestructive

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/features/internal/infra"
	"github.com/DeBrosOfficial/network/e2e/harness"
	"github.com/DeBrosOfficial/network/e2e/harness/fleet"
	"github.com/DeBrosOfficial/network/e2e/harness/oramacli"
	"github.com/DeBrosOfficial/network/e2e/harness/wallet"
	"github.com/DeBrosOfficial/network/pkg/invite"
)

const (
	// uploadedArchive is where the build is copied before it is extracted.
	uploadedArchive = "/root/e2e-orama.tar.gz"
	// secretsFile carries the invite to the install's stdin, 0600, removed at cleanup.
	secretsFile = "/root/e2e-join-secrets.json"
	// nodeCLI is the CLI the extracted archive carries.
	nodeCLI = infra.OramaBinDir + "/orama"
	// replayPublicIP is a public address no node of the run has (TEST-NET-3).
	replayPublicIP = "203.0.113.78"
)

// stageOnServer puts the build where a manual join expects it, the operator
// step docs/CLI_REFERENCE.md "orama maint node install" names: "The build archive
// must be extracted at /opt/orama".
func stageOnServer(t testing.TB, f *fleet.Fleet, n fleet.Node, archive string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), infra.InstallBudget)
	defer cancel()
	data := mustRead(t, archive)
	if err := f.SSHFor(t, n).Put(ctx, uploadedArchive, data, 0o600); err != nil {
		t.Fatalf("upload the archive to %s: %v", n.Name, err)
	}
	f.MustExec(t, n, "install -d -m 0755 -o root -g root /opt/orama && tar --no-same-owner -xzf "+uploadedArchive+
		" -C /opt/orama && rm -f "+uploadedArchive)
}

// installWith runs `orama maint node install` on n with the invite on stdin
// (--secrets-stdin: the invite never appears on a command line).
func installWith(t testing.TB, f *fleet.Fleet, n fleet.Node, encoded string, extra ...string) fleet.Output {
	t.Helper()
	raw, err := json.Marshal(map[string]string{"token": encoded})
	if err != nil {
		t.Fatal(err)
	}
	f.WriteFile(t, n, secretsFile, raw, 0o600)
	args := append([]string{"maint", "node", "install", "--secrets-stdin", "--vps-ip", n.PublicIP,
		"--base-domain", f.State.BaseDomain, "--environment", f.State.Env}, extra...)
	cmd := nodeCLI + " " + strings.TrimPrefix(infra.OramaCommand(args...), infra.OramaBinOnNode+" ") + " < " + secretsFile
	ctx, cancel := context.WithTimeout(t.Context(), infra.InstallBudget)
	defer cancel()
	out, err := f.SSHFor(t, n).Run(ctx, cmd)
	if err != nil {
		t.Fatalf("run the install on %s: %v", n.Name, err)
	}
	return out
}

func mintInvite(t testing.TB, f *fleet.Fleet) string {
	t.Helper()
	var m struct {
		Invite string `json:"invite"`
	}
	res := harness.CLI(t).MustOK(t, "maint", "invite", "--env", f.State.Env, "--node", f.State.Nodes[0].PublicIP, "--json")
	if err := oramacli.DecodeJSON(res, &m); err != nil {
		t.Fatal(err)
	}
	return m.Invite
}

// TestJoin_manualJoinSpendsTheInviteOnce drives the manual join an operator
// runs on the new machine and every property of the invite along the way:
// an invite whose pin is wrong is refused before the token is sent; a joiner
// expecting other archive signers is refused before the token is spent; the
// genuine invite, on stdin, joins the node as a full member; the row records
// who used it; and the same invite is refused as used afterwards
// (docs/SECURITY.md "TLS & Transport", "Supply Chain", "Secrets never on a
// command line"; handlers/join tokenRefusal).
func TestJoin_manualJoinSpendsTheInviteOnce(t *testing.T) {
	f := harness.Fleet(t)
	infra.RequireHealthy(t)
	extra := infra.NewExtra(t, "extra-join")
	t.Cleanup(func() { infra.RemoveIfMember(t, extra.PublicIP) })
	stageOnServer(t, f, extra.Node, infra.RunningArchive(t, f))
	encoded := mintInvite(t, f)
	inv := infra.DecodeInvite(t, encoded)
	minter := f.State.Nodes[0]

	refusedJoinsKeepTheInvite(t, f, extra.Node, encoded, minter)
	requireUnused(t, f, minter, inv.Token)
	out := installWith(t, f, extra.Node, encoded)
	if out.Exit != 0 {
		t.Fatalf("the manual join failed (exit %d):\n%s", out.Exit, f.Redact(out.Stdout+out.Stderr))
	}
	if strings.Contains(out.Stdout+out.Stderr, inv.Token) {
		t.Error("the install printed the invite token")
	}
	infra.WaitConverged(t, len(f.State.Nodes)+1, infra.ConvergeBudget, "the manually joined node to be a full member")
	row := infra.ReadInvite(t, f, minter, inv.Token)
	if !row.Used || row.UsedBy != extra.PublicIP {
		t.Errorf("after the join the invite row is %+v, want used by %s", row, extra.PublicIP)
	}
	infra.ForgetPhantomOnCleanup(t, replayPublicIP)
	r := infra.Join(t, minter, infra.JoinBody{Token: inv.Token, WGPublicKey: infra.NewWGKey(t), PublicIP: replayPublicIP})
	if r.Status != http.StatusUnauthorized || !strings.Contains(string(r.Body), infra.JoinUsed) {
		t.Errorf("replaying the spent invite: HTTP %d %.200q, want 401 %q", r.Status, r.Body, infra.JoinUsed)
	}
}

// refusedJoinsKeepTheInvite: an invite whose pin is wrong is refused before
// the token is sent, and a joiner expecting other archive signers before the
// token is spent; neither uses the invite.
func refusedJoinsKeepTheInvite(t *testing.T, f *fleet.Fleet, joiner fleet.Node, encoded string, minter fleet.Node) {
	inv := infra.DecodeInvite(t, encoded)
	t.Run("wrongPinRefused", func(t *testing.T) {
		forged := inv
		forged.CAFingerprint = strings.Repeat("0", len(inv.CAFingerprint))
		bad, err := invite.Encode(forged)
		if err != nil {
			t.Fatal(err)
		}
		out := installWith(t, f, joiner, bad)
		if out.Exit == 0 || !strings.Contains(out.Stdout+out.Stderr, "fingerprint mismatch") {
			t.Fatalf("an invite pinning the wrong certificate: exit %d\n%s", out.Exit, f.Redact(out.Stdout+out.Stderr))
		}
		requireUnused(t, f, minter, inv.Token)
	})
	t.Run("otherSignersRefused", func(t *testing.T) {
		stranger, err := wallet.NewEVM()
		if err != nil {
			t.Fatal(err)
		}
		out := installWith(t, f, joiner, encoded, "--expect-archive-signers", stranger.Address())
		// The archive preflight refuses it, before any join request:
		// "the build archive ... cannot be installed, so the join was not
		// requested" (pkg/install/archive_signers.go preflightArchive). Any
		// other failure (a flag, the network) is not the check under test.
		if out.Exit == 0 || !strings.Contains(out.Stdout+out.Stderr, "so the join was not requested") {
			t.Fatalf("a joiner expecting %s: exit %d, want the archive preflight's refusal:\n%s", stranger.Address(), out.Exit, f.Redact(out.Stdout+out.Stderr))
		}
		requireUnused(t, f, minter, inv.Token)
	})
}

func mustRead(t testing.TB, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return data
}

// requireUnused fails unless the invite's row is there and not spent: a
// refused join must leave the invite usable.
func requireUnused(t testing.TB, f *fleet.Fleet, minter fleet.Node, token string) {
	t.Helper()
	if row := infra.ReadInvite(t, f, minter, token); !row.Found || row.Used {
		t.Fatalf("a refused join spent the invite: %+v", row)
	}
}
