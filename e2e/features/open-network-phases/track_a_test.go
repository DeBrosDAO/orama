//go:build e2e_fleet

package opennetworkphases

import (
	"net/http"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/features/internal/infra"
	"github.com/DeBrosOfficial/network/e2e/features/internal/tenancy"
	"github.com/DeBrosOfficial/network/e2e/harness"
	"github.com/DeBrosOfficial/network/e2e/harness/fleet"
	"github.com/DeBrosOfficial/network/e2e/harness/gw"
	"github.com/DeBrosOfficial/network/e2e/harness/ns"
	"github.com/DeBrosOfficial/network/e2e/harness/oramacli"
)

const (
	trackA = "plans/open-network/track-a-private-clusters.md"
	// creationSetting prefixes the namespace-creation line of
	// `orama cluster settings show`.
	creationSetting = "namespace-creation: "
)

// TestPhaseA1_genesisWalletOperatesTheCluster: the wallet that created the
// cluster is on its operator list with no SQL by hand, and an operator route
// refuses a wallet that is not (A1).
func TestPhaseA1_genesisWalletOperatesTheCluster(t *testing.T) {
	phase(t, "A1", "docs/CLI_REFERENCE.md", "### orama operator add", trackA+" A1")
	f := harness.Fleet(t)
	list := run(t, harness.CLI(t), "operator", "list")
	if list.Exit != exitOK || !strings.Contains(strings.ToLower(out(list)), strings.ToLower(f.State.OperatorAddress)) {
		t.Fatalf("the genesis operator %s is not on the operator list (exit %d):\n%s", f.State.OperatorAddress, list.Exit, out(list))
	}
	stranger := gw.NewUser(t, f, gw.LobbyNamespace)
	r := harness.GW(t).MustSend(t, gw.Req{Path: "/v1/operator/health", Bearer: stranger.Token()})
	tenancy.ExpectDenied(t, r, "an operator route as a wallet that is not an operator")
}

// TestPhaseA2_freshCLIPointsAtNoCluster: a CLI that was never configured
// knows no environment (nobody's devnet or testnet) and says how to add
// one (A2; core/cmd/orama/internal/environment.go noEnvironmentHelp).
func TestPhaseA2_freshCLIPointsAtNoCluster(t *testing.T) {
	phase(t, "A2", "docs/CLI_REFERENCE.md", "### orama env add", trackA+" A2")
	cli := freshCLI(t)
	listed := strings.ToLower(out(run(t, cli, "env", "list")))
	for _, fleetName := range []string{"devnet", "testnet", "mainnet", "stagenet"} {
		if strings.Contains(listed, fleetName) {
			t.Errorf("a fresh CLI lists the owner's %s environment:\n%s", fleetName, listed)
		}
	}
	cur := run(t, cli, "env", "current")
	if cur.Exit == exitOK || !strings.Contains(out(cur), "orama env add") {
		t.Errorf("`orama env current` on a fresh CLI: exit %d, want a refusal naming `orama env add`:\n%s", cur.Exit, out(cur))
	}
}

// TestPhaseA3_creationPolicyEnforced: with namespace creation set to
// operators, a wallet that is not one is refused before anything is
// created; the setting is shown back, and open is restored and read back
// (A3; the bootstrap contract keeps the run open). A slot is held for the
// namespace a broken policy would let through, and an accepted one is
// adopted so it is deleted.
func TestPhaseA3_creationPolicyEnforced(t *testing.T) {
	phase(t, "A3", "docs/CLI_REFERENCE.md", "### orama cluster settings set", trackA+" A3")
	f := harness.Fleet(t)
	cli := harness.CLI(t)
	tenancy.Reserve(t, f, 1)
	t.Cleanup(func() { restoreOpen(t, f, cli) })
	cli.MustOK(t, "cluster", "settings", "set", "namespace-creation", "operators")
	if shown := cli.MustOK(t, "cluster", "settings", "show").Stdout; !strings.Contains(shown, creationSetting+"operators") {
		t.Fatalf("settings show after set:\n%s", shown)
	}
	stranger := gw.NewUser(t, f, gw.LobbyNamespace)
	r := tenancy.Create(t, stranger, ns.UniqueName(t.Name()))
	if r.Status >= http.StatusOK && r.Status < http.StatusMultipleChoices {
		var c tenancy.Created
		if err := r.Decode(&c); err == nil && c.ClusterID != "" {
			tenancy.Adopt(t, f, stranger, c)
		}
		t.Fatalf("a non-operator created a namespace under the operators policy: HTTP %d %s", r.Status, r.Body)
	}
	if r.Status != http.StatusForbidden || r.ErrorCode() != "NAMESPACE_CREATION_DENIED" {
		t.Fatalf("a non-operator's creation under the operators policy: HTTP %d %s, want 403 NAMESPACE_CREATION_DENIED", r.Status, r.Body)
	}
}

// restoreOpen sets namespace creation back to open and reads it back, from a
// cleanup: f and cli are captured before it is registered (harness.CLI
// refuses once the run is interrupted).
func restoreOpen(t *testing.T, f *fleet.Fleet, cli *oramacli.Runner) {
	ctx, cancel := fleet.CleanupContext(t)
	defer cancel()
	res, err := cli.Run(ctx, "cluster", "settings", "set", "namespace-creation", "open")
	if err != nil || res.Exit != exitOK {
		t.Errorf("cleanup: restoring namespace-creation open on %s: %v %s — later stages cannot create namespaces", f.State.Env, err, res.Stderr)
		return
	}
	shown, err := cli.Run(ctx, "cluster", "settings", "show")
	if err != nil || shown.Exit != exitOK || !strings.Contains(shown.Stdout, creationSetting+"open") {
		t.Errorf("cleanup: namespace-creation on %s does not read back open (exit %d): %v\n%s — later stages cannot create namespaces",
			f.State.Env, shown.Exit, err, shown.Stdout)
	}
}

// TestPhaseA4_releaseRootRefusesBeforeExtracting: asked to check a release
// against the TUF release root, stage-archive refuses an archive whose
// metadata does not verify, even though the archive itself is the one the
// node's wallet anchor trusts, and /opt/orama is untouched (A4;
// docs/CLI_REFERENCE.md "orama node stage-archive").
func TestPhaseA4_releaseRootRefusesBeforeExtracting(t *testing.T) {
	phase(t, "A4", "docs/CLI_REFERENCE.md", "--release-metadata", trackA+" A4")
	f := harness.Fleet(t)
	n := f.State.Nodes[0]
	archive := infra.RunningArchive(t, f)
	remote := "/var/tmp/e2e-a4-" + f.State.RunID + ".tar.gz"
	f.WriteFile(t, n, remote, readLocal(t, archive), 0o600)
	metaDir := "/var/tmp/e2e-a4-meta-" + f.State.RunID
	t.Cleanup(func() { cleanupPath(t, f, n, metaDir) })
	f.MustExec(t, n, "mkdir -m 0700 "+metaDir)
	before := f.MustExec(t, n, "stat -c '%i %Y' "+infra.StagedManifest).Stdout
	res := onNode(t, f, n, "node", "stage-archive", "--archive", remote, "--release-metadata", metaDir, "--release-target", "orama-linux-amd64.tar.gz")
	expectVerifyRefusal(t, f, n, res)
	if !strings.Contains(res.Stdout+res.Stderr, "release root: ") {
		t.Errorf("stage-archive's refusal does not come from the release root check:\n%s", f.Redact(res.Stdout+res.Stderr))
	}
	if after := f.MustExec(t, n, "stat -c '%i %Y' "+infra.StagedManifest).Stdout; after != before {
		t.Errorf("a refused archive changed %s (%s -> %s)", infra.StagedManifest, before, after)
	}
}

// TestPhaseA5_notifyByDefaultValidatorNeverAuto: the update decision
// reports a newer release without installing it unless the cluster chose
// auto, and a validator cannot be auto (A5; the unattended agent that would
// act on the decision is not in this release, docs/CLI_REFERENCE.md "orama
// node autoupdate": "The install itself ... is not performed by this command").
func TestPhaseA5_notifyByDefaultValidatorNeverAuto(t *testing.T) {
	phase(t, "A5", "docs/CLI_REFERENCE.md", "### orama node autoupdate", trackA+" A5")
	cli := harness.CLI(t)
	def := run(t, cli, "node", "autoupdate", "--current", "1.0.0", "--candidate", "1.0.1")
	if def.Exit != exitOK || strings.TrimSpace(out(def)) != "notify: newer release 1.0.1 (notify)" {
		t.Errorf("the default decision: exit %d %q", def.Exit, out(def))
	}
	v := run(t, cli, "node", "autoupdate", "--current", "1.0.0", "--candidate", "1.0.1", "--mode", "auto", "--role", "validator")
	if v.Exit != exitUsage {
		t.Errorf("auto for a validator: exit %d %q, want the usage refusal", v.Exit, out(v))
	}
}
